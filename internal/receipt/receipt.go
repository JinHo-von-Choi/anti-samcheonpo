// Package receipt renders receipts in text, markdown and JSON.
package receipt

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/seal"
)

// SymptomName maps symptom codes to plain Korean names.
var SymptomName = map[string]string{
	"S1": "검증 쳇바퀴", "S2": "실패 루프", "S3": "맥락 이탈", "S4": "헛바퀴 탐색", "S5": "거짓 완료와 우회",
	"S6": "역량 한계", "S7": "기억 부패", "S8": "비용 폭주", "revert": "되돌림",
}

var agentName = map[string]string{"claude": "Claude Code", "codex": "Codex"}

var gradeName = map[string]string{analyze.GradeVerified: "검증", analyze.GradeEstimated: "추정", analyze.GradeUnmeasured: "측정 불가"}

// Line is one receipt line.
type Line struct {
	Key    string  `json:"key"`
	Label  string  `json:"label"`
	Micro  int64   `json:"micro_krw"`
	Won    int64   `json:"won"`
	Tokens int64   `json:"tokens"`
	Pct    float64 `json:"pct"`
	Note   string  `json:"note,omitempty"`
	Indent int     `json:"indent"`
}

// Receipt is the renderable model.
type Receipt struct {
	Billing       cost.Measurement      `json:"billing"`
	Action        ActionSummary         `json:"action"`
	Title         string                `json:"title"`
	Notices       []string              `json:"notices"`
	Unit          string                `json:"unit"` // won | tokens | unknown
	PriceCoverage float64               `json:"price_coverage"`
	Minutes       int                   `json:"minutes"`
	Total         Line                  `json:"total"`
	Lines         []Line                `json:"lines"`
	Estimated     *Line                 `json:"estimated,omitempty"`
	Footer        []string              `json:"footer"`
	Seal          string                `json:"seal,omitempty"`
	Grade         string                `json:"grade"`
	Evidence      map[string][]Evidence `json:"evidence,omitempty"`
}

// Evidence is one expanded verdict for --evidence.
type Evidence struct {
	Rule   string  `json:"rule"`
	Level  string  `json:"level"`
	Conf   float64 `json:"confidence"`
	Seqs   []int64 `json:"seqs"`
	Detail string  `json:"detail"`
}

// Input bundles what a receipt is built from.
type Input struct {
	Billing    cost.Observation
	Recoveries []recovery.Attempt
	Title      string
	Agent      string
	Audit      bool
	Totals     analyze.Totals
	Grade      string
	Verdicts   []detect.Signal
	Seal       *seal.Seal
	Criteria   [2]int
	Summaries  map[int64]string // seq -> event summary (for evidence)
}

// Build creates a receipt model.
func Build(in Input) Receipt {
	t := in.Totals
	billing := cost.Measure(in.Billing, t.Micro, t.Tokens, t.UnpricedTokens)
	r := Receipt{Title: in.Title, PriceCoverage: billing.PriceCoverage, Minutes: t.Minutes, Grade: in.Grade, Billing: billing, Action: SummarizeAction(in)}
	r.Action.Unknowns = append(r.Action.Unknowns, billing.Missing...)
	r.Unit = "won"
	if r.PriceCoverage < 1 {
		r.Unit = "tokens"
	}
	if billing.ObservedTokens == nil {
		r.Unit = "unknown"
	}
	if in.Audit {
		r.Notices = append(r.Notices, "소급 분석이라 맥락 이탈은 추정입니다")
	}
	if billing.UsageObserved {
		r.Notices = append(r.Notices, fmt.Sprintf("API 단가 환산 · 단가 인식률 %d%%", int(r.PriceCoverage*100+0.5)))
	}
	r.Notices = append(r.Notices, "환산액은 실제 청구액이나 절감액이 아닙니다")
	if billing.Kind == cost.BillingSubscription {
		r.Notices = append(r.Notices, "구독형 과금: 토큰·한도와 API 환산액을 구분합니다. 실제 추가 청구액은 미확인입니다")
	}
	if billing.Kind == cost.BillingUnknown {
		r.Notices = append(r.Notices, "과금 유형 미확인: 한도 정보만으로 구독 결제를 추정하지 않습니다")
	}
	if r.Unit == "tokens" {
		r.Notices = append(r.Notices, "단가 누락이 있어 토큰 기준으로 표시합니다")
	}
	if billing.Quota != nil {
		r.Notices = append(r.Notices, fmt.Sprintf("보고된 한도: %s 창 %.0f%% 사용", window(billing.Quota.WindowMinutes), billing.Quota.UsedPct))
	}
	val := func(m, tok int64) (int64, float64) {
		if r.Unit == "won" {
			return m, float64(m)
		}
		return tok, float64(tok)
	}
	totalV, totalF := val(t.Micro, t.Tokens)
	_ = totalV
	pct := func(m, tok int64) float64 {
		_, f := val(m, tok)
		if totalF == 0 {
			return 0
		}
		return f / totalF * 100
	}
	r.Total = Line{Key: "total", Label: "총 API 환산액 (계측분)", Micro: t.Micro, Tokens: t.Tokens, Pct: 100}
	if r.Unit == "tokens" {
		r.Total.Label = "계측된 토큰"
	}
	if r.Unit == "unknown" {
		r.Total.Label = "사용량 미확인"
		r.Total.Pct = 0
	}
	if r.Minutes > 0 {
		r.Total.Note = "(" + duration(r.Minutes) + ")"
	}
	grade := gradeName[in.Grade]
	add := func(key, label string, m, tok int64, indent int, note string) {
		r.Lines = append(r.Lines, Line{Key: key, Label: label, Micro: m, Tokens: tok, Pct: pct(m, tok), Indent: indent, Note: note})
	}
	add("progress", "진척 ("+grade+")", t.BucketMicro[analyze.BucketProgress], t.BucketTokens[analyze.BucketProgress], 1, "")
	add("explore", "필요한 탐색", t.BucketMicro[analyze.BucketExplore], t.BucketTokens[analyze.BucketExplore], 1, "")
	add("waste", "헛짓", t.BucketMicro[analyze.BucketWaste], t.BucketTokens[analyze.BucketWaste], 1, "")
	for _, s := range analyze.SortedSymptoms(t) {
		add("waste."+s, SymptomName[s], t.SymptomMicro[s], t.SymptomTokens[s], 2, symptomNote(s, t, in.Verdicts))
	}
	add("other", "기타 작업", t.BucketMicro[analyze.BucketOther], t.BucketTokens[analyze.BucketOther], 1, "")
	add("watch", "감시 비용", t.BucketMicro[analyze.BucketWatch], t.BucketTokens[analyze.BucketWatch], 1, "")
	if t.EstimatedMicro > 0 || t.EstimatedTokens > 0 {
		l := Line{Key: "estimated", Label: "확인 필요", Micro: t.EstimatedMicro, Tokens: t.EstimatedTokens, Pct: pct(t.EstimatedMicro, t.EstimatedTokens),
			Note: "추정 판정(범위 밖 수정 등), 위 분류에 이미 포함"}
		r.Estimated = &l
	}
	r.Total.Won = cost.Won(t.Micro)
	var topIdx, childIdx []int
	var top, child []int64
	wasteIdx := -1
	for i, l := range r.Lines {
		if l.Indent == 1 {
			topIdx = append(topIdx, i)
			top = append(top, l.Micro)
			if l.Key == "waste" {
				wasteIdx = i
			}
		} else {
			childIdx = append(childIdx, i)
			child = append(child, l.Micro)
		}
	}
	for j, w := range cost.RoundTo(top, r.Total.Won) {
		r.Lines[topIdx[j]].Won = w
	}
	if wasteIdx >= 0 {
		for j, w := range cost.RoundTo(child, r.Lines[wasteIdx].Won) {
			r.Lines[childIdx[j]].Won = w
		}
	}
	if r.Estimated != nil {
		r.Estimated.Won = cost.Won(r.Estimated.Micro)
	}
	var fl []string
	if in.Criteria[1] > 0 {
		fl = append(fl, fmt.Sprintf("완료 조건 %d개 중 %d개 충족", in.Criteria[1], in.Criteria[0]))
	}
	iv := t.Interventions
	n := 0
	var parts []string
	for _, k := range []string{"L1", "L2", "L3", "L4"} {
		if iv[k] > 0 {
			n += iv[k]
			parts = append(parts, fmt.Sprintf("%s %d", levelName(k), iv[k]))
		}
	}
	if n > 0 {
		label := "개입 후보"
		if in.Audit {
			label = "실시간이었다면 개입"
		}
		fl = append(fl, fmt.Sprintf("%s %d회 (%s)", label, n, strings.Join(parts, ", ")))
	}
	r.Footer = fl
	if in.Seal != nil {
		r.Seal = in.Seal.Short()
	}
	return r
}

func levelName(k string) string {
	switch k {
	case "L1":
		return "귀띔"
	case "L2":
		return "알림"
	case "L3":
		return "일시정지"
	case "L4":
		return "인수인계 권고"
	}
	return k
}

func window(min int) string {
	switch {
	case min >= 10080:
		return "주간"
	case min >= 300:
		return fmt.Sprintf("%d시간", min/60)
	}
	return fmt.Sprintf("%d분", min)
}

func duration(min int) string {
	if min >= 60 {
		return fmt.Sprintf("%d시간 %d분", min/60, min%60)
	}
	return fmt.Sprintf("%d분", min)
}

// symptomNote describes the strongest verdict of a symptom.
func symptomNote(sym string, t analyze.Totals, vs []detect.Signal) string {
	if sym == "revert" {
		return fmt.Sprintf("이후 되돌려진 수정 %d건", t.SymptomCount[sym])
	}
	var best *detect.Signal
	for i := range vs {
		v := &vs[i]
		if v.Detector != sym {
			continue
		}
		if best == nil || v.WasteMicro > best.WasteMicro || (v.WasteMicro == best.WasteMicro && v.Level > best.Level) {
			best = v
		}
	}
	if best == nil {
		return ""
	}
	return Describe(*best)
}

// Describe renders a short plain description of a verdict.
func Describe(v detect.Signal) string {
	f := v.Facts
	str := func(k string) string { s, _ := f[k].(string); return s }
	num := func(k string) int {
		switch x := f[k].(type) {
		case int:
			return x
		case float64:
			return int(x)
		case int64:
			return int(x)
		}
		return 0
	}
	switch v.Rule {
	case "s1.identical_rerun":
		return fmt.Sprintf("같은 검증 %d회, 그 사이 코드 변화 없음 (%s)", num("count"), short(str("cmd"), 40))
	case "s1.evidence_rerun":
		return fmt.Sprintf("유효한 통과 근거가 있는 검사를 다시 실행하려 함 (%s)", short(str("cmd"), 40))
	case "s1.verify_after_docs":
		return fmt.Sprintf("문서만 바뀐 뒤 같은 결과로 재검증 (%s)", short(str("cmd"), 40))
	case "s1.review_repeat":
		return fmt.Sprintf("코드가 그대로인데 같은 검토 %d회 (%s)", num("count"), short(str("purpose"), 40))
	case "s1.verify_ratio":
		return fmt.Sprintf("최근 %d번 중 검증 %d번, 만들기 %d번", num("window"), num("verify"), num("produce"))
	case "s1.test_bloat":
		return fmt.Sprintf("시험 코드 %d줄, 기능 코드 %d줄", num("test_lines"), num("code_lines"))
	case "s2.stuck_error":
		return fmt.Sprintf("고쳐도 같은 오류 %d회", num("count"))
	case "s2.attempt_repeat":
		return fmt.Sprintf("이미 실패한 변경을 다시 쓰려 함 (%s)", short(str("path"), 40))
	case "s2.environment":
		if str("frozen_path") != "" {
			return fmt.Sprintf("코드 밖 원인이 그대로인데 파일 수정 시도 (%s)", short(str("frozen_path"), 40))
		}
		return fmt.Sprintf("코드로 고칠 수 없는 원인으로 같은 명령 %d회 실패 (%s)", num("count"), short(str("cmd"), 40))
	case "s2.oscillation":
		return fmt.Sprintf("%s를 이전 상태로 되돌림", short(str("path"), 40))
	case "s2.semantic_oscillation":
		return fmt.Sprintf("%s가 이름·공백만 다른 채 이미 실패한 상태로 돌아옴", short(str("path"), 40))
	case "s2.whack_a_mole":
		return fmt.Sprintf("오류는 바뀌는데 실패 %d건이 %d번 시도 동안 줄지 않음", num("failed"), num("attempts"))
	case "s3.out_of_scope":
		return fmt.Sprintf("요청 범위 밖 파일 수정 (%s)", short(str("path"), 40))
	case "s3.config_bypass":
		if str("kind") == "dependency" {
			return "의존성 파일 변경 (" + short(str("path"), 40) + ")"
		}
		return "검사·빌드 설정 변경 (" + short(str("path"), 40) + ")"
	case "s1.full_suite_local_change":
		return fmt.Sprintf("%s만 바꾸고 전체 시험 재실행 (%s)", str("size"), short(str("cmd"), 40))
	case "s1.unprobed_long_run":
		return fmt.Sprintf("짧은 확인 없이 긴 실행 시작 (%s)", short(str("cmd"), 40))
	case "s2.verifier_deadlock":
		if str("kind") == "waiting" {
			return fmt.Sprintf("같은 거부가 반복된 채 사용자 선택을 기다리며 멈춤 (%s)", short(str("cmd"), 40))
		}
		return fmt.Sprintf("같은 도구가 같은 이유로 %d회 거부 (%s)", num("count"), short(str("cmd"), 40))
	case "s2.flaky_ui_race":
		return fmt.Sprintf("브라우저 시험이 화면 준비 전 확인으로 %d회 실패 (%s)", num("count"), short(str("cmd"), 40))
	case "s4.serial_triage":
		return fmt.Sprintf("보고서 항목을 하나씩 처리 %d회 (%s)", num("cycles"), short(str("path"), 40))
	case "s5.release_without_preflight":
		switch str("kind") {
		case "ci_failed":
			return fmt.Sprintf("원격 CI 실패를 로컬에서 확인하지 않고 배포 (%s)", short(str("cmd"), 40))
		case "unverified_change":
			return fmt.Sprintf("직전 배포 뒤 고친 내용을 검사하지 않고 다시 배포 (%s)", short(str("cmd"), 40))
		}
		return fmt.Sprintf("브라우저 시험 실패가 남은 채 배포 (%s)", short(str("cmd"), 40))
	case "s5.release_rate":
		return fmt.Sprintf("한 시간 안에 %d번째 배포 (%s)", num("count")+1, short(str("cmd"), 40))
	case "s8.session_long":
		return fmt.Sprintf("세션 활동 %s, 토큰 %s", str("hours"), str("tokens"))
	case "s8.session_ceiling":
		return fmt.Sprintf("세션 상한 %s 도달", str("limit"))
	case "s4.read_only_streak":
		return fmt.Sprintf("만들지 않고 읽기만 %d번", num("count"))
	case "s5.test_weakening":
		return "시험 약화: " + kindName(str("kind")) + " (" + short(str("path"), 40) + ")"
	case "s5.error_hiding":
		return "오류 숨김: " + kindName(str("kind"))
	case "s5.false_done":
		switch str("kind") {
		case "failed":
			return "검증 실패 상태에서 완료 선언"
		case "stale":
			return "마지막 검증 뒤에 코드를 고치고 완료 선언"
		case "unverified":
			return "검증 없이 완료 선언"
		case "unmet":
			return "완료 조건 미충족 상태에서 완료 선언"
		}
	case "s5.answer_copy":
		return "시험의 기대값을 기능 코드에 그대로 넣음"
	case "s7.memory_rot":
		return fmt.Sprintf("대화 압축 %d회, 같은 파일 재열람 %d회", num("compactions"), num("rereads"))
	case "s8.velocity":
		return fmt.Sprintf("분당 %s원, 평소의 3배 이상", contract.Comma(int64(num("per_min_krw"))))
	case "s8.budget":
		return fmt.Sprintf("예산 %d%% 도달", num("pct"))
	case "s8.idle_spend":
		return fmt.Sprintf("진척 없이 %s원", contract.Comma(int64(num("idle_krw"))))
	case "s8.forced_no_progress":
		return fmt.Sprintf("자동 계속이 진척 없이 %d번", num("count"))
	case "s3.drift":
		return "목표와 관계없는 작업으로 두 번 연속 판정"
	case "s6.capability_limit":
		if v.Facts["same_files"] == true {
			return fmt.Sprintf("같은 파일만 %d번 고쳤지만 같은 지점에서 계속 실패", num("attempts"))
		}
		return fmt.Sprintf("다른 접근 %d가지가 같은 지점에서 모두 실패", num("strategies"))
	case "s5.stop_unmet":
		return fmt.Sprintf("완료 조건 %d개 실패 상태에서 종료 시도", num("count"))
	case "s3.protected_path":
		return "보호 경로 수정 시도 (" + short(str("path"), 40) + ")"
	}
	return v.Rule
}

func kindName(k string) string {
	switch k {
	case "test_deleted":
		return "시험 파일 삭제"
	case "test_removed":
		return "시험 함수 삭제"
	case "skip_added":
		return "skip 추가"
	case "assertion_mutilation":
		return "단언문 무력화"
	case "assertion_weakened":
		return "단언 약화"
	case "literal_replaced":
		return "기대값 교체"
	case "ignore_added":
		return "무시 주석 추가"
	case "empty_catch":
		return "빈 예외 처리"
	case "or_true":
		return "|| true로 실패 무시"
	case "test_infra_changed":
		return "시험 실행 설정 변경"
	}
	return k
}

func short(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Width returns the display width (East Asian wide chars count 2).
func Width(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r >= 0x1100 && r <= 0x115F, r >= 0x2E80 && r <= 0x303E, r >= 0x3041 && r <= 0x33FF,
		r >= 0x3400 && r <= 0x4DBF, r >= 0x4E00 && r <= 0x9FFF, r >= 0xA000 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3, r >= 0xF900 && r <= 0xFAFF, r >= 0xFE30 && r <= 0xFE4F,
		r >= 0xFF00 && r <= 0xFF60, r >= 0xFFE0 && r <= 0xFFE6, r >= 0x3130 && r <= 0x318F:
		return 2
	}
	return 1
}

func pad(s string, w int) string {
	if d := w - Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func lpad(s string, w int) string {
	if d := w - Width(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

func (r Receipt) amount(l Line) string {
	if r.Unit == "unknown" {
		return "미확인"
	}
	if r.Unit == "tokens" {
		return HumanTokens(l.Tokens)
	}
	return contract.Comma(l.Won) + "원"
}

// HumanTokens formats a token count.
func HumanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM토큰", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK토큰", float64(n)/1e3)
	}
	return fmt.Sprintf("%d토큰", n)
}

// Text renders the receipt as aligned plain text.
func (r Receipt) Text() string {
	var b strings.Builder
	b.WriteString(r.Title + "\n")
	for _, n := range r.Notices {
		b.WriteString(n + "\n")
	}
	b.WriteString(r.Action.Text())
	lw := 24
	aw := 14
	fmt.Fprintf(&b, "%s%s   %s\n", pad(r.Total.Label, lw), lpad(r.amount(r.Total), aw), r.Total.Note)
	for _, l := range r.Lines {
		if l.Indent == 1 && l.Key == "watch" && l.Micro == 0 && l.Tokens == 0 {
			continue
		}
		label := strings.Repeat("  ", l.Indent) + l.Label
		pct := fmt.Sprintf("%3.0f%%", l.Pct)
		if r.Unit == "unknown" {
			pct = "—"
		}
		note := ""
		if l.Note != "" {
			note = "  " + l.Note
		}
		fmt.Fprintf(&b, "%s%s   %s%s\n", pad(label, lw), lpad(r.amount(l), aw), pct, note)
	}
	if r.Estimated != nil {
		fmt.Fprintf(&b, "%s%s   %s\n", pad(r.Estimated.Label, lw), lpad(r.amount(*r.Estimated), aw), r.Estimated.Note)
	}
	for _, f := range r.Footer {
		b.WriteString(f + "\n")
	}
	if r.Seal != "" {
		b.WriteString("봉인 " + r.Seal + "\n")
	}
	for _, k := range sortedKeys(r.Evidence) {
		b.WriteString("\n근거 · " + SymptomNameOr(k) + "\n")
		for _, e := range r.Evidence[k] {
			fmt.Fprintf(&b, "  [%s %s %.2f] %s\n", e.Level, e.Rule, e.Conf, e.Detail)
		}
	}
	return b.String()
}

// SymptomNameOr returns the plain name or the code.
func SymptomNameOr(k string) string {
	if n, ok := SymptomName[k]; ok {
		return n
	}
	return k
}

func sortedKeys(m map[string][]Evidence) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// Markdown renders the receipt as a markdown table.
func (r Receipt) Markdown() string {
	var b strings.Builder
	b.WriteString("## " + r.Title + "\n\n")
	for _, n := range r.Notices {
		b.WriteString("- " + n + "\n")
	}
	b.WriteString("\n### 지금 할 판단\n\n")
	for _, line := range strings.Split(strings.TrimSpace(r.Action.Text()), "\n") {
		b.WriteString("- " + mdEsc(line) + "\n")
	}
	b.WriteString("\n| 항목 | 금액 | 비율 | 메모 |\n| --- | ---: | ---: | --- |\n")
	percent := func(n float64) string {
		if r.Unit == "unknown" {
			return "—"
		}
		return fmt.Sprintf("%.0f%%", n)
	}
	fmt.Fprintf(&b, "| **%s** | **%s** | %s | %s |\n", r.Total.Label, r.amount(r.Total), percent(r.Total.Pct), r.Total.Note)
	for _, l := range r.Lines {
		if l.Key == "watch" && l.Micro == 0 && l.Tokens == 0 {
			continue
		}
		fmt.Fprintf(&b, "| %s%s | %s | %s | %s |\n", strings.Repeat("&nbsp;&nbsp;", l.Indent-1), l.Label, r.amount(l), percent(l.Pct), mdEsc(l.Note))
	}
	if r.Estimated != nil {
		fmt.Fprintf(&b, "| %s | %s | %.0f%% | %s |\n", r.Estimated.Label, r.amount(*r.Estimated), r.Estimated.Pct, r.Estimated.Note)
	}
	b.WriteString("\n")
	for _, f := range r.Footer {
		b.WriteString("- " + f + "\n")
	}
	if r.Seal != "" {
		b.WriteString("- 봉인 `" + r.Seal + "`\n")
	}
	for _, k := range sortedKeys(r.Evidence) {
		b.WriteString("\n### 근거 · " + SymptomNameOr(k) + "\n\n")
		for _, e := range r.Evidence[k] {
			fmt.Fprintf(&b, "- `%s` %s (확신도 %.2f): %s\n", e.Rule, e.Level, e.Conf, mdEsc(e.Detail))
		}
	}
	return b.String()
}

func mdEsc(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// JSON renders the receipt model.
func (r Receipt) JSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b) + "\n"
}

// Share returns a copy without evidence details or command text (--share).
func (r Receipt) Share() Receipt {
	s := r
	s.Evidence = nil
	s.Action = ActionSummary{Unknowns: []string{"공유본에서는 작업별 관측·추정·다음 행동의 상세 내용을 제외했다"}}
	s.Lines = make([]Line, len(r.Lines))
	for i, l := range r.Lines {
		l.Note = ""
		if l.Indent == 2 {
			l.Note = ""
		}
		s.Lines[i] = l
	}
	return s
}

// WithEvidence attaches expanded verdict evidence.
func (r *Receipt) WithEvidence(vs []detect.Signal, summaries map[int64]string) {
	r.Evidence = map[string][]Evidence{}
	for _, v := range vs {
		if v.Suppressed {
			continue
		}
		d := Describe(v)
		var ss []string
		for i, sq := range v.Evidence {
			if i >= 6 {
				ss = append(ss, fmt.Sprintf("외 %d건", len(v.Evidence)-6))
				break
			}
			if s, ok := summaries[sq]; ok {
				ss = append(ss, fmt.Sprintf("#%d %s", sq, s))
			} else {
				ss = append(ss, fmt.Sprintf("#%d", sq))
			}
		}
		if len(ss) > 0 {
			d += " ← " + strings.Join(ss, "; ")
		}
		r.Evidence[v.Detector] = append(r.Evidence[v.Detector], Evidence{Rule: v.Rule, Level: v.Level.String(), Conf: v.Confidence, Seqs: v.Evidence, Detail: d})
	}
}

// SessionTitle builds the title line of a session receipt.
func SessionTitle(started time.Time, id, agent string) string {
	d := ""
	if !started.IsZero() {
		d = started.Local().Format("2006-01-02 15:04") + " "
	}
	sid := id
	if len(sid) > 8 {
		sid = sid[:8]
	}
	a := agentName[agent]
	if a == "" {
		a = agent
	}
	return fmt.Sprintf("삼천포 영수증 · %s세션 %s · %s", d, sid, a)
}
