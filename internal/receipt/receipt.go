// Package receipt renders receipts in text, markdown and JSON.
package receipt

import (
	"encoding/json"
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
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
	"S1": lang.T("검증 쳇바퀴"), "S2": lang.T("실패 루프"), "S3": lang.T("맥락 이탈"), "S4": lang.T("헛바퀴 탐색"), "S5": lang.T("거짓 완료·우회·검증 없는 배포"),
	"S6": lang.T("역량 한계"), "S7": lang.T("기억 부패"), "S8": lang.T("비용 폭주"), "revert": lang.T("되돌림"),
}

var agentName = map[string]string{"claude": "Claude Code", "codex": "Codex"}

var gradeName = map[string]string{analyze.GradeVerified: lang.T("검증"), analyze.GradeEstimated: lang.T("추정"), analyze.GradeUnmeasured: lang.T("측정 불가")}

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

// Estimate is a labelled estimate.
//
// It never claims a saving. It says what the blocked repetitions would have
// cost if they had kept going, and it names the assumption it rests on. The
// measured part of a receipt stays separate from this part, so a reader can
// tell what was counted from what was assumed.
type Estimate struct {
	// Kind is always "estimate".
	Kind string `json:"kind"`
	// Label names the estimate in one line.
	Label string `json:"label"`
	// Basis says what was counted, in facts.
	Basis string `json:"basis"`
	// Assumption says what the estimate assumes, including the sample.
	Assumption string `json:"assumption"`
	// Count is the number of stopped repetitions.
	Count int `json:"count"`
	// Tokens and Micro are the estimated size of the stopped repetitions.
	Tokens int64 `json:"tokens,omitempty"`
	Micro  int64 `json:"micro_krw,omitempty"`
}

// Receipt is the renderable model.
type Receipt struct {
	Billing       cost.Measurement `json:"billing"`
	Action        ActionSummary    `json:"action"`
	Title         string           `json:"title"`
	Notices       []string         `json:"notices"`
	Unit          string           `json:"unit"` // won | tokens | unknown
	PriceCoverage float64          `json:"price_coverage"`
	Minutes       int              `json:"minutes"`
	Total         Line             `json:"total"`
	Lines         []Line           `json:"lines"`
	Estimated     *Line            `json:"estimated,omitempty"`
	// Estimate is the labelled estimate of the stopped repetitions. It is
	// empty when the observation was cut short, because a cut short
	// observation cannot carry an estimate.
	Estimate *Estimate             `json:"estimate,omitempty"`
	Footer   []string              `json:"footer"`
	Seal     string                `json:"seal,omitempty"`
	Grade    string                `json:"grade"`
	Evidence map[string][]Evidence `json:"evidence,omitempty"`
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
	// Observation states what the analysis could not cover. It never
	// changes a number; it says which numbers are unknown.
	Observation analyze.Observation
	// Blocked counts the tool calls the monitor stopped before they ran.
	Blocked int
	// Estimate is the labelled estimate of what the stopped repetitions
	// would have cost. It stays empty when the observation was cut short.
	Estimate *Estimate
	// JudgeSkippedLowRisk counts the tool calls that were left out of the
	// semantic judge's budget because they carried low risk. It is a
	// report of budget saved, not a count of defects.
	JudgeSkippedLowRisk int
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
		r.Notices = append(r.Notices, lang.T("소급 분석이라 맥락 이탈은 추정입니다"))
	}
	// A lost or partial filesystem watch does not change any number here.
	// It says which numbers cannot be read as complete.
	switch in.Observation.WatchState {
	case "unavailable":
		r.Notices = append(r.Notices, lang.T("파일 감시를 잃어 작업 폴더 지문은 미확인이다"))
	case "degraded":
		r.Notices = append(r.Notices, lang.T("일부 디렉터리만 감시해 작업 폴더 지문이 일부만 반영된다"))
	}
	// A learned rule pack that could not be read leaves the learned rules
	// out of the session. Saying so keeps "no rule fired" honest.
	switch in.Observation.CandidatePack {
	case "unreadable":
		r.Notices = append(r.Notices, lang.T("학습 규칙 파일을 읽지 못해 학습 규칙을 적용하지 않았다"))
	case "invalid":
		r.Notices = append(r.Notices, lang.T("학습 규칙 파일 형식이 올바르지 않아 학습 규칙을 적용하지 않았다"))
	}
	if billing.UsageObserved {
		r.Notices = append(r.Notices, fmt.Sprintf(lang.T("API 단가 환산 · 단가 인식률 %d%%"), int(r.PriceCoverage*100+0.5)))
	}
	r.Notices = append(r.Notices, lang.T("환산액은 실제 청구액이나 절감액이 아닙니다"))
	if in.Estimate != nil {
		e := *in.Estimate
		e.Kind = "estimate"
		r.Estimate = &e
		r.Notices = append(r.Notices, lang.T("추정치는 관측값이 아니라 가정을 바탕으로 한 값입니다"))
	}
	if billing.Kind == cost.BillingSubscription {
		r.Notices = append(r.Notices, lang.T("구독형 과금: 토큰·한도와 API 환산액을 구분합니다. 실제 추가 청구액은 미확인입니다"))
	}
	if billing.Kind == cost.BillingUnknown {
		r.Notices = append(r.Notices, lang.T("과금 유형 미확인: 한도 정보만으로 구독 결제를 추정하지 않습니다"))
	}
	if r.Unit == "tokens" {
		r.Notices = append(r.Notices, lang.T("단가 누락이 있어 토큰 기준으로 표시합니다"))
	}
	if t.UnpricedTokens > 0 {
		r.Notices = append(r.Notices, fmt.Sprintf(lang.T("가격표에 없는 모델의 토큰 %d개는 가격 미산정으로 따로 셉니다"), t.UnpricedTokens))
	}
	if billing.Quota != nil {
		r.Notices = append(r.Notices, fmt.Sprintf(lang.T("보고된 한도: %s 창 %.0f%% 사용"), window(billing.Quota.WindowMinutes), billing.Quota.UsedPct))
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
	r.Total = Line{Key: "total", Label: lang.T("총 API 환산액 (계측분)"), Micro: t.Micro, Tokens: t.Tokens, Pct: 100}
	if r.Unit == "tokens" {
		r.Total.Label = lang.T("계측된 토큰")
	}
	if r.Unit == "unknown" {
		r.Total.Label = lang.T("사용량 미확인")
		r.Total.Pct = 0
	}
	if r.Minutes > 0 {
		r.Total.Note = "(" + duration(r.Minutes) + ")"
	}
	grade := gradeName[in.Grade]
	add := func(key, label string, m, tok int64, indent int, note string) {
		r.Lines = append(r.Lines, Line{Key: key, Label: label, Micro: m, Tokens: tok, Pct: pct(m, tok), Indent: indent, Note: note})
	}
	add("progress", lang.T("진척 (")+grade+")", t.BucketMicro[analyze.BucketProgress], t.BucketTokens[analyze.BucketProgress], 1, "")
	add("explore", lang.T("필요한 탐색"), t.BucketMicro[analyze.BucketExplore], t.BucketTokens[analyze.BucketExplore], 1, "")
	add("waste", lang.T("헛짓"), t.BucketMicro[analyze.BucketWaste], t.BucketTokens[analyze.BucketWaste], 1, "")
	for _, s := range analyze.SortedSymptoms(t) {
		add("waste."+s, SymptomName[s], t.SymptomMicro[s], t.SymptomTokens[s], 2, symptomNote(s, t, in.Verdicts))
	}
	add("other", lang.T("기타 작업"), t.BucketMicro[analyze.BucketOther], t.BucketTokens[analyze.BucketOther], 1, "")
	add("watch", lang.T("감시 비용"), t.BucketMicro[analyze.BucketWatch], t.BucketTokens[analyze.BucketWatch], 1, "")
	if t.EstimatedMicro > 0 || t.EstimatedTokens > 0 {
		l := Line{Key: "estimated", Label: lang.T("확인 필요"), Micro: t.EstimatedMicro, Tokens: t.EstimatedTokens, Pct: pct(t.EstimatedMicro, t.EstimatedTokens),
			Note: lang.T("추정 판정(범위 밖 수정 등), 위 분류에 이미 포함")}
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
		fl = append(fl, fmt.Sprintf(lang.T("완료 조건 %d개 중 %d개 충족"), in.Criteria[1], in.Criteria[0]))
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
		label := lang.T("개입 후보")
		if in.Audit {
			label = lang.T("실시간이었다면 개입")
		}
		fl = append(fl, fmt.Sprintf(lang.T("%s %d회 (%s)"), label, n, strings.Join(parts, ", ")))
	}
	if in.Blocked > 0 {
		// A block is an action, not a candidate. Counting it separately
		// keeps "how many did the monitor catch" apart from "how many did
		// it judge".
		fl = append(fl, fmt.Sprintf(lang.T("실행 전 차단 %d회"), in.Blocked))
	}
	if in.JudgeSkippedLowRisk > 0 {
		// The judge is spent where the behavior can do damage. This says
		// how much of the budget was left alone.
		fl = append(fl, fmt.Sprintf(lang.T("위험 등급이 낮은 도구 호출 %d건을 의미 판정에서 제외"), in.JudgeSkippedLowRisk))
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
		return lang.T("귀띔")
	case "L2":
		return lang.T("알림")
	case "L3":
		return lang.T("일시정지")
	case "L4":
		return lang.T("인수인계 권고")
	}
	return k
}

func window(min int) string {
	switch {
	case min >= 10080:
		return lang.T("주간")
	case min >= 300:
		return fmt.Sprintf(lang.T("%d시간"), min/60)
	}
	return fmt.Sprintf(lang.T("%d분"), min)
}

func duration(min int) string {
	if min >= 60 {
		return fmt.Sprintf(lang.T("%d시간 %d분"), min/60, min%60)
	}
	return fmt.Sprintf(lang.T("%d분"), min)
}

// symptomNote describes the strongest verdict of a symptom.
func symptomNote(sym string, t analyze.Totals, vs []detect.Signal) string {
	if sym == "revert" {
		return fmt.Sprintf(lang.T("이후 되돌려진 수정 %d건"), t.SymptomCount[sym])
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
	case "s1.explicit_waiting":
		return fmt.Sprintf(lang.T("명시적 대기 %d회 · 추정(%s)"), num("count"), str("eligibility"))
	case "s8.progress_stall":
		return fmt.Sprintf(lang.T("관련 검사 %s에서 개선 없는 실패 %d회 · 추정"), str("check_id"), num("failures"))
	case "s1.identical_rerun":
		return fmt.Sprintf(lang.T("같은 검증 %d회, 그 사이 코드 변화 없음 (%s)"), num("count"), short(str("cmd"), 40))
	case "s1.evidence_rerun":
		return fmt.Sprintf(lang.T("유효한 통과 근거가 있는 검사를 다시 실행하려 함 (%s)"), short(str("cmd"), 40))
	case "s1.verify_after_docs":
		return fmt.Sprintf(lang.T("문서만 바뀐 뒤 같은 결과로 재검증 (%s)"), short(str("cmd"), 40))
	case "s1.review_repeat":
		return fmt.Sprintf(lang.T("코드가 그대로인데 같은 검토 %d회 (%s)"), num("count"), short(str("purpose"), 40))
	case "s1.verify_streak":
		return fmt.Sprintf(lang.T("진전 없는 통과 검사 %d회 연속 · 추정"), num("count"))
	case "s1.verify_ratio":
		return fmt.Sprintf(lang.T("최근 %d번 중 검증 %d번, 만들기 %d번"), num("window"), num("verify"), num("produce"))
	case "s1.test_bloat":
		return fmt.Sprintf(lang.T("시험 코드 %d줄, 기능 코드 %d줄"), num("test_lines"), num("code_lines"))
	case "s2.stuck_error":
		return fmt.Sprintf(lang.T("고쳐도 같은 오류 %d회"), num("count"))
	case "s2.attempt_repeat":
		return fmt.Sprintf(lang.T("이미 실패한 변경을 다시 쓰려 함 (%s)"), short(str("path"), 40))
	case "s2.environment":
		if str("frozen_path") != "" {
			return fmt.Sprintf(lang.T("코드 밖 원인이 그대로인데 파일 수정 시도 (%s)"), short(str("frozen_path"), 40))
		}
		return fmt.Sprintf(lang.T("코드로 고칠 수 없는 원인으로 같은 명령 %d회 실패 (%s)"), num("count"), short(str("cmd"), 40))
	case "s2.oscillation":
		return fmt.Sprintf(lang.T("%s를 이전 상태로 되돌림"), short(str("path"), 40))
	case "s2.semantic_oscillation":
		return fmt.Sprintf(lang.T("%s가 이름·공백만 다른 채 이미 실패한 상태로 돌아옴"), short(str("path"), 40))
	case "s2.whack_a_mole":
		return fmt.Sprintf(lang.T("오류는 바뀌는데 실패 %d건이 %d번 시도 동안 줄지 않음"), num("failed"), num("attempts"))
	case "s3.out_of_scope":
		return fmt.Sprintf(lang.T("요청 범위 밖 파일 수정 (%s)"), short(str("path"), 40))
	case "s3.config_bypass":
		if str("kind") == "dependency" {
			return lang.T("의존성 파일 변경 (") + short(str("path"), 40) + ")"
		}
		return lang.T("검사·빌드 설정 변경 (") + short(str("path"), 40) + ")"
	case "s8.leftover_process":
		return fmt.Sprintf(lang.T("세션이 작업 폴더에서 띄운 프로세스 %d개가 남음"), num("count"))
	case "s1.revert_reverify":
		return fmt.Sprintf(lang.T("수정을 되돌려 실패를 확인한 뒤 복원해 다시 검사 %d회 (%s)"), num("count"), short(str("cmd"), 40))
	case "s5.artifact_meta":
		if str("kind") == "filler" {
			return fmt.Sprintf(lang.T("문서에 지워도 되는 문장 %d개 (%s)"), num("count"), short(str("path"), 40))
		}
		if str("kind") == "publish" {
			return fmt.Sprintf(lang.T("공개될 글에 작업 경위를 적은 문장 %d개 (%s)"), num("count"), str("via"))
		}
		return fmt.Sprintf(lang.T("문서·주석에 작업 경위를 적은 문장 %d개 (%s)"), num("count"), short(str("path"), 40))
	case "s5.answer_padding":
		return fmt.Sprintf(lang.T("답변에 결과와 관계없는 문장 %d개 (%s)"), num("count"), str("kinds"))
	case "s3.unrequested_ui_action":
		if str("issue") == "dead_target" {
			return fmt.Sprintf(lang.T("이동할 곳이 없는 링크 %d개 추가 (%s)"), num("count"), short(str("path"), 40))
		}
		if str("issue") == "filler_card" {
			return fmt.Sprintf(lang.T("글자만 있는 안내 카드 %d개 추가 (%s)"), num("count"), short(str("path"), 40))
		}
		if str("issue") == "repeated_cta" {
			return fmt.Sprintf(lang.T("같은 문구의 버튼·링크 %d개 추가 (%s)"), num("count"), short(str("path"), 40))
		}
		if str("element") != "" {
			return fmt.Sprintf(lang.T("%s %d개 추가, 요청은 %d개 (%s)"), str("element"), num("count"), num("requested"), short(str("path"), 40))
		}
		return fmt.Sprintf(lang.T("요청 없이 버튼·링크 %d개 추가 (%s)"), num("count"), short(str("path"), 40))
	case "s5.flake_masking":
		return fmt.Sprintf(lang.T("실패 원인 대신 재시도·시간 한도를 바꿈 (%s, %s)"), str("kinds"), short(str("path"), 40))
	case "s8.host_resource":
		switch str("kind") {
		case "artifacts":
			return fmt.Sprintf(lang.T("빌드 산출물이 세션 중 %vGB 늘어 %vGB"), f["grown_gb"], f["total_gb"])
		case "free":
			return fmt.Sprintf(lang.T("디스크 여유 공간 %vGB(%d%%)"), f["free_gb"], num("free_pct"))
		}
		return fmt.Sprintf(lang.T("검사 실행 시간 누적 %d분"), num("minutes"))
	case "s8.host_budget":
		return fmt.Sprintf(lang.T("계약 예산 초과(%s / %s)로 무거운 검사 중단"), str("spent"), str("budget"))
	case "s5.deploy_without_preflight":
		switch str("kind") {
		case "ci_failed":
			return fmt.Sprintf(lang.T("원격 CI 실패를 로컬에서 확인하지 않고 운영 배포 (%s)"), short(str("cmd"), 40))
		case "ui_timing":
			return fmt.Sprintf(lang.T("화면 시험이 실패한 채 운영 배포 (%s)"), short(str("cmd"), 40))
		}
		return fmt.Sprintf(lang.T("마지막 코드 변경 뒤 검사 없이 운영 배포 (%s)"), short(str("cmd"), 40))
	case "s3.production_write", "s3.production_guard":
		return fmt.Sprintf(lang.T("운영 데이터베이스 쓰기 (%s, %s)"), str("where"), short(str("cmd"), 40))
	case "s3.migration_history_write":
		return fmt.Sprintf(lang.T("마이그레이션 이력 테이블을 손으로 변경 (%s)"), short(str("cmd"), 40))
	case "s1.concurrent_shared_resource":
		return lang.T("다른 실행이 같은 공유 자원을 사용 중이다. 쓰기는 직렬 실행하거나 독립 자원을 지정한다. 독립 읽기는 계속할 수 있다.")
	case "s1.concurrent_heavy_check":
		return fmt.Sprintf(lang.T("다른 무거운 시험이 %d분째 실행 중인데 무거운 시험 추가 시작 (%s)"), num("since"), short(str("cmd"), 40))
	case "s1.no_change_rerun":
		return fmt.Sprintf(lang.T("변경 없이 통과한 검사를 다시 실행하려 함 (%s)"), short(str("cmd"), 40))
	case "s1.test_growth":
		return fmt.Sprintf(lang.T("요청 없이 시험 코드 %d줄, 기능 코드 %d줄에서 시험을 더 쓰려 함"), num("test_lines"), num("code_lines"))
	case "s5.scope_dropped":
		return lang.T("요청 범위를 임의로 줄인 채 끝내려 함")
	case "s5.buried_remainder":
		return lang.T("남은 일을 뒤에 적고 끝난 것처럼 보고함")
	case "s5.unobserved_result":
		return fmt.Sprintf(lang.T("화면 파일 %d개를 고치고 실제 화면을 확인하지 않음"), num("count"))
	case "s5.critique_required":
		return fmt.Sprintf(lang.T("코드 %d줄을 바꾸고 독립 검토 없이 끝내려 함"), num("lines"))
	case "s1.full_suite_budget":
		return fmt.Sprintf(lang.T("요청 하나에 전체 시험 %d번째 시도 (%s)"), num("runs")+1, short(str("cmd"), 40))
	case "s1.full_suite_local_change":
		return fmt.Sprintf(lang.T("%s만 바꾸고 전체 시험 재실행 (%s)"), str("size"), short(str("cmd"), 40))
	case "s1.unprobed_long_run":
		return fmt.Sprintf(lang.T("짧은 확인 없이 긴 실행 시작 (%s)"), short(str("cmd"), 40))
	case "s2.verifier_deadlock":
		if str("kind") == "waiting" {
			return fmt.Sprintf(lang.T("같은 거부가 반복된 채 사용자 선택을 기다리며 멈춤 (%s)"), short(str("cmd"), 40))
		}
		return fmt.Sprintf(lang.T("같은 도구가 같은 이유로 %d회 거부 (%s)"), num("count"), short(str("cmd"), 40))
	case "s2.flaky_ui_race":
		return fmt.Sprintf(lang.T("브라우저 시험이 화면 준비 전 확인으로 %d회 실패 (%s)"), num("count"), short(str("cmd"), 40))
	case "s4.serial_triage":
		return fmt.Sprintf(lang.T("보고서 항목을 하나씩 처리 %d회 (%s)"), num("cycles"), short(str("path"), 40))
	case "s5.release_without_preflight":
		switch str("kind") {
		case "ci_failed":
			return fmt.Sprintf(lang.T("원격 CI 실패를 로컬에서 확인하지 않고 배포 (%s)"), short(str("cmd"), 40))
		case "unverified_change":
			return fmt.Sprintf(lang.T("직전 배포 뒤 고친 내용을 검사하지 않고 다시 배포 (%s)"), short(str("cmd"), 40))
		}
		return fmt.Sprintf(lang.T("브라우저 시험 실패가 남은 채 배포 (%s)"), short(str("cmd"), 40))
	case "s5.release_rate":
		return fmt.Sprintf(lang.T("한 시간 안에 %d번째 배포 (%s)"), num("count")+1, short(str("cmd"), 40))
	case "s8.session_long":
		return fmt.Sprintf(lang.T("세션 활동 %s, 토큰 %s"), str("hours"), str("tokens"))
	case "s8.session_ceiling":
		return fmt.Sprintf(lang.T("세션 상한 %s 도달"), str("limit"))
	case "s4.read_only_streak":
		return fmt.Sprintf(lang.T("만들지 않고 읽기만 %d번"), num("count"))
	case "s5.test_weakening":
		return lang.T("시험 약화: ") + kindName(str("kind")) + " (" + short(str("path"), 40) + ")"
	case "s5.error_hiding":
		return lang.T("오류 숨김: ") + kindName(str("kind"))
	case "s5.false_done":
		switch str("kind") {
		case "failed":
			return lang.T("검증 실패 상태에서 완료 선언")
		case "stale":
			return lang.T("마지막 검증 뒤에 코드를 고치고 완료 선언")
		case "unverified":
			return lang.T("검증 없이 완료 선언")
		case "unmet":
			return lang.T("완료 조건 미충족 상태에서 완료 선언")
		case "hollow":
			return lang.T("미구현 본문이 남은 채 완료 선언")
		case "no_evidence":
			return lang.T("요청에 적힌 대상을 바꾸지 않고 완료 선언")
		case "narrow_check":
			return lang.T("새로 만든 시험만 실행하고 완료 선언")
		}
	case "s3.fixation":
		return lang.T("한 항목에 몰린 채 요청한 다른 대상을 방치")
	case "s5.abandoned_verification":
		switch str("kind") {
		case "failing_left":
			return lang.T("실패한 검사를 고치지 않고 작업 종료")
		case "no_check_after_change":
			return lang.T("코드 변경 뒤 검사 없이 작업 종료")
		case "unmet_left":
			return lang.T("완료 조건 실패 상태로 작업 종료")
		}
	case "s5.answer_copy":
		return lang.T("시험의 기대값을 기능 코드에 그대로 넣음")
	case "s7.memory_rot":
		return fmt.Sprintf(lang.T("대화 압축 %d회, 같은 파일 재열람 %d회"), num("compactions"), num("rereads"))
	case "s8.velocity":
		return fmt.Sprintf(lang.T("분당 %s원, 평소의 3배 이상"), contract.Comma(int64(num("per_min_krw"))))
	case "s8.budget":
		return fmt.Sprintf(lang.T("예산 %d%% 도달"), num("pct"))
	case "s8.idle_spend":
		return fmt.Sprintf(lang.T("진척 없이 %s원"), contract.Comma(int64(num("idle_krw"))))
	case "s8.forced_no_progress":
		return fmt.Sprintf(lang.T("자동 계속이 진척 없이 %d번"), num("count"))
	case "s3.drift":
		return lang.T("목표와 관계없는 작업으로 두 번 연속 판정")
	case "s6.capability_limit":
		if v.Facts["same_files"] == true {
			return fmt.Sprintf(lang.T("같은 파일만 %d번 고쳤지만 같은 지점에서 계속 실패"), num("attempts"))
		}
		return fmt.Sprintf(lang.T("다른 접근 %d가지가 같은 지점에서 모두 실패"), num("strategies"))
	case "s5.stop_unmet":
		return fmt.Sprintf(lang.T("완료 조건 %d개 실패 상태에서 종료 시도"), num("count"))
	case "s3.protected_path":
		return lang.T("보호 경로 수정 시도 (") + short(str("path"), 40) + ")"
	case "s9.instruction_smuggling":
		return lang.T("감시자를 속이려는 지시가 컨텍스트에 있음 (") + str("source") + ")"
	case "s9.unverified_claim_request":
		return lang.T("미수행 검사를 보고하라는 지시가 컨텍스트에 있음 (") + str("source") + ")"
	case "s10.grader_access":
		return lang.T("채점기·정답 경로 접근 시도 (") + short(str("path"), 40) + ")"
	}
	return v.Rule
}

func kindName(k string) string {
	switch k {
	case "test_deleted":
		return lang.T("시험 파일 삭제")
	case "test_removed":
		return lang.T("시험 함수 삭제")
	case "skip_added":
		return lang.T("skip 추가")
	case "assertion_mutilation":
		return lang.T("단언문 무력화")
	case "assertion_weakened":
		return lang.T("단언 약화")
	case "literal_replaced":
		return lang.T("기대값 교체")
	case "ignore_added":
		return lang.T("무시 주석 추가")
	case "empty_catch":
		return lang.T("빈 예외 처리")
	case "or_true":
		return lang.T("|| true로 실패 무시")
	case "test_infra_changed":
		return lang.T("시험 실행 설정 변경")
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
		return lang.T("미확인")
	}
	if r.Unit == "tokens" {
		return HumanTokens(l.Tokens)
	}
	return contract.Comma(l.Won) + lang.T("원")
}

// HumanTokens formats a token count.
func HumanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf(lang.T("%.1fM토큰"), float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf(lang.T("%.1fK토큰"), float64(n)/1e3)
	}
	return fmt.Sprintf(lang.T("%d토큰"), n)
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
		pct := cost.PercentageText(l.Pct)
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
	if r.Estimate != nil {
		b.WriteString(lang.T("\n추정 · 관측값과 구분한다\n"))
		fmt.Fprintf(&b, "  %s\n", r.Estimate.Label)
		fmt.Fprintf(&b, lang.T("  근거(관측): %s\n"), r.Estimate.Basis)
		fmt.Fprintf(&b, lang.T("  가정: %s\n"), r.Estimate.Assumption)
		if r.Estimate.Tokens > 0 {
			fmt.Fprintf(&b, lang.T("  추정 크기: %s\n"), HumanTokens(r.Estimate.Tokens))
		}
	}
	for _, f := range r.Footer {
		b.WriteString(f + "\n")
	}
	if r.Seal != "" {
		b.WriteString(lang.T("봉인 ") + r.Seal + "\n")
	}
	for _, k := range sortedKeys(r.Evidence) {
		b.WriteString(lang.T("\n근거 · ") + SymptomNameOr(k) + "\n")
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
	b.WriteString(lang.T("\n### 지금 할 판단\n\n"))
	for _, line := range strings.Split(strings.TrimSpace(r.Action.Text()), "\n") {
		b.WriteString("- " + mdEsc(line) + "\n")
	}
	b.WriteString(lang.T("\n| 항목 | 금액 | 비율 | 메모 |\n| --- | ---: | ---: | --- |\n"))
	percent := func(n float64) string {
		if r.Unit == "unknown" {
			return "—"
		}
		return cost.PercentageText(n)
	}
	fmt.Fprintf(&b, "| **%s** | **%s** | %s | %s |\n", r.Total.Label, r.amount(r.Total), percent(r.Total.Pct), r.Total.Note)
	for _, l := range r.Lines {
		if l.Key == "watch" && l.Micro == 0 && l.Tokens == 0 {
			continue
		}
		fmt.Fprintf(&b, "| %s%s | %s | %s | %s |\n", strings.Repeat("&nbsp;&nbsp;", l.Indent-1), l.Label, r.amount(l), percent(l.Pct), mdEsc(l.Note))
	}
	if r.Estimated != nil {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", r.Estimated.Label, r.amount(*r.Estimated), cost.PercentageText(r.Estimated.Pct), r.Estimated.Note)
	}
	if r.Estimate != nil {
		b.WriteString(lang.T("\n### 추정 · 관측값과 구분\n\n"))
		fmt.Fprintf(&b, "- **%s**\n", mdEsc(r.Estimate.Label))
		fmt.Fprintf(&b, lang.T("- 근거(관측): %s\n"), mdEsc(r.Estimate.Basis))
		fmt.Fprintf(&b, lang.T("- 가정: %s\n"), mdEsc(r.Estimate.Assumption))
		if r.Estimate.Tokens > 0 {
			fmt.Fprintf(&b, lang.T("- 추정 크기: %s\n"), HumanTokens(r.Estimate.Tokens))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	for _, f := range r.Footer {
		b.WriteString("- " + f + "\n")
	}
	if r.Seal != "" {
		b.WriteString(lang.T("- 봉인 `") + r.Seal + "`\n")
	}
	for _, k := range sortedKeys(r.Evidence) {
		b.WriteString(lang.T("\n### 근거 · ") + SymptomNameOr(k) + "\n\n")
		for _, e := range r.Evidence[k] {
			fmt.Fprintf(&b, lang.T("- `%s` %s (확신도 %.2f): %s\n"), e.Rule, e.Level, e.Conf, mdEsc(e.Detail))
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
	s.Action = ActionSummary{Unknowns: []string{lang.T("공유본에서는 작업별 관측·추정·다음 행동의 상세 내용을 제외했다")}}
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
				ss = append(ss, fmt.Sprintf(lang.T("외 %d건"), len(v.Evidence)-6))
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
	return fmt.Sprintf(lang.T("삼천포 영수증 · %s세션 %s · %s"), d, sid, a)
}
