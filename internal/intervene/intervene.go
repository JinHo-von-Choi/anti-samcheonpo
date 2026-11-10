// Package intervene renders agent nudges and user messages from verdicts.
package intervene

import (
	"bytes"
	"embed"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"text/template"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
)

//go:embed templates/ko/*.tmpl templates/en/*.tmpl
var tmplFS embed.FS

// Languages lists the languages messages are written in.
var Languages = []string{lang.Korean, lang.English}

var tmplsOnce = sync.OnceValue(func() map[string]*template.Template {
	out := make(map[string]*template.Template, len(Languages))
	for _, l := range Languages {
		out[l] = template.Must(template.New("").Option("missingkey=zero").ParseFS(tmplFS, "templates/"+l+"/*.tmpl"))
	}
	return out
})

// Context supplies session facts for messages.
type Context struct {
	UsageUnknown bool
	Goal         string
	Scope        string
	IdleMicro    int64
	TotalMicro   int64
	UnitTokens   bool
	IdleTokens   int64
	TotalTokens  int64
	Handoff      string
	// Lang is the language of the message; empty means Korean.
	Lang string
	// Currency is "KRW" or "USD"; empty follows the language.
	Currency string
	// USDKRW converts amounts to dollars; 0 keeps them in won.
	USDKRW float64
}

func (c Context) english() bool { return c.Lang == lang.English }

func (c Context) pick(ko, en string) string {
	if c.english() {
		return en
	}
	return ko
}

func (c Context) dollars() bool {
	if c.USDKRW <= 0 || c.Currency == "KRW" {
		return false
	}
	return c.Currency == "USD" || c.english()
}

// Amount renders a whole-won amount in the session's currency.
func (c Context) Amount(krw int64) string {
	switch {
	case c.dollars():
		return fmt.Sprintf("$%.2f", float64(krw)/c.USDKRW)
	case c.english():
		return contract.Comma(krw) + " KRW"
	}
	return contract.Comma(krw) + "원"
}

func (c Context) apiAmount(krw int64) string {
	return c.pick("API 환산 ", "API-equivalent ") + c.Amount(krw)
}

// Choices is the fixed user choice line.
const Choices = "[/samcheonpo:keep now 이번만 허용]  [/samcheonpo:keep normal 이 판정은 틀림]  [/samcheonpo:steer 방향 바꾸라고 하기]  [/samcheonpo:summary 멈추고 요약 받기]"

// ChoicesEnglish is Choices in English.
const ChoicesEnglish = "[/samcheonpo:keep now allow once]  [/samcheonpo:keep normal this verdict is wrong]  [/samcheonpo:steer tell it to change direction]  [/samcheonpo:summary stop and get a summary]"

func RecoveryMarker(id string) string { return "[sc-recovery:" + id + "]" }

// Recovery renders a recovery prescription in the given language.
func Recovery(a recovery.Attempt, l string) string {
	p := recovery.Localize(a.Prescription, l)
	return render(l, "recovery.agent.tmpl", map[string]any{"marker": RecoveryMarker(a.ID), "action": p.Action, "stop": p.StopCondition})
}

func (c Context) won(micro int64) string {
	return c.apiAmount((micro + 500_000) / 1_000_000)
}

var kindNames = map[string]string{
	"test_deleted": "시험 파일 삭제", "test_removed": "시험 함수 삭제", "skip_added": "skip 표시 추가",
	"assertion_weakened": "단언 약화", "literal_replaced": "기대값 교체", "ignore_added": "무시 주석",
	"empty_catch": "빈 예외 처리", "or_true": "`|| true`로 실패 무시", "test_infra_changed": "시험 실행 설정 변경",
	"assertion_mutilation": "단언문 삭제·약화·skip 주입",
	"dependency":           "의존성 파일", "config": "검사·빌드 설정 파일",
	"unresolvable_host": "접속 주소를 찾을 수 없음", "service_down": "접속할 서비스가 꺼져 있음", "dependency_missing": "설치되지 않은 의존성",
	"permission": "권한 없음", "transient": "일시적인 네트워크 장애", "port_in_use": "포트를 다른 프로그램이 쓰고 있음",
	"auth_rejected": "인증 거부", "command_missing": "설치되지 않은 명령", "not_executable": "실행 권한이 없는 파일", "disk_full": "디스크 공간 부족",
	"mock_substituted": "시험의 실제 외부 서비스를 가짜로 대체", "stale_goal": "이전 목표의 경로로 되돌아감", "guessed": "요청에서 추정한 범위 밖",
	"bypass_monitor": "감시 우회 지시", "hide_report": "보고 은폐 지시", "fake_result": "미수행 검사 보고 지시",
	"failing_left": "마지막 검사가 실패한 채로 남아 있다", "no_check_after_change": "마지막 코드 변경 뒤에 실행한 검사가 없다",
	"unmet_left": "완료 조건 일부가 실패한 채로 남아 있다",
}

var kindNamesEnglish = map[string]string{
	"test_deleted": "test file deleted", "test_removed": "test function deleted", "skip_added": "skip marker added",
	"assertion_weakened": "assertion weakened", "literal_replaced": "expected value replaced", "ignore_added": "ignore comment",
	"empty_catch": "empty exception handler", "or_true": "failure ignored with `|| true`", "test_infra_changed": "test runner settings changed",
	"assertion_mutilation": "assertion deleted, weakened or skipped",
	"dependency":           "dependency file", "config": "check or build settings file",
	"unresolvable_host": "host name cannot be resolved", "service_down": "the service to connect to is down", "dependency_missing": "dependency not installed",
	"permission": "permission denied", "transient": "temporary network failure", "port_in_use": "port in use by another program",
	"auth_rejected": "authentication rejected", "command_missing": "command not installed", "not_executable": "file is not executable", "disk_full": "disk full",
	"mock_substituted": "real external service in a test replaced with a fake", "stale_goal": "returned to the path of an earlier goal", "guessed": "outside the scope inferred from the request",
	"bypass_monitor": "instruction to bypass the monitor", "hide_report": "instruction to hide a report", "fake_result": "instruction to report checks not run",
	"failing_left": "the last check is still failing", "no_check_after_change": "no check was run after the last code change",
	"unmet_left": "some completion criteria are still failing",
}

// sourceNames names where a context instruction came from.
var sourceNames = map[string]string{
	"prompt": "사용자 프롬프트", "message": "에이전트 메시지", "tool_output": "도구 출력", "written": "에이전트가 쓴 파일",
}

var sourceNamesEnglish = map[string]string{
	"prompt": "the user prompt", "message": "an agent message", "tool_output": "tool output", "written": "a file the agent wrote",
}

// englishFacts rewrites the Korean values detectors put in a finding's facts.
var englishFacts = sync.OnceValue(func() []struct {
	re *regexp.Regexp
	to string
} {
	type rule = struct {
		re *regexp.Regexp
		to string
	}
	rules := []rule{}
	for _, r := range [][2]string{
		{`파일 (\d+)개\(스크립트로 고쳐 줄 수 미확인\)`, "$1 files (scripted edit, line count unknown)"},
		{`파일 (\d+)개 (\d+)줄`, "$1 files, $2 lines"},
		{`요청 (\d+)줄, 답변 (\d+)줄`, "$1 lines requested, $2 lines answered"},
		{`작업 순서를 적은 문장 (\d+)개`, "$1 sentences narrating work steps"},
		{`(\d+)시간 (\d+)분`, "${1}h ${2}m"},
		{`(\d+)시간`, "${1}h"},
		{`(\d+)분`, "${1}m"},
		{`\(포트 ([0-9, ]+)\)`, "(port $1)"},
		{` 경과`, " elapsed"},
		{` → 고친 문장: `, " → rewrite: "},
		{` → 삭제`, " → delete"},
		{` 또는 `, " or "},
		{` 토큰`, " tokens"},
		{`버튼`, "buttons"},
		{`링크`, "links"},
	} {
		rules = append(rules, rule{regexp.MustCompile(r[0]), r[1]})
	}
	return rules
})

// FactText renders a detector's Korean value in the given language.
func FactText(l, s string) string {
	if l != lang.English {
		return s
	}
	for _, r := range englishFacts() {
		s = r.re.ReplaceAllString(s, r.to)
	}
	return s
}

func data(v detect.Signal, c Context) map[string]any {
	d := map[string]any{}
	for k, x := range v.Facts {
		d[k] = x
	}
	ev := make([]string, 0, len(v.Evidence))
	for i, s := range v.Evidence {
		if i >= 8 {
			ev = append(ev, "…")
			break
		}
		ev = append(ev, fmt.Sprint(s))
	}
	d["evidence"] = strings.Join(ev, ", ")
	if c.english() {
		for _, k := range []string{"size", "hours", "limit", "tokens", "element", "sentences", "procs", "candidates"} {
			if x, ok := d[k].(string); ok {
				d[k] = FactText(lang.English, x)
			}
		}
	}
	goal := c.Goal
	if goal == "" {
		goal = c.pick("최신 요청에 적힌 작업", "the work in the latest request")
	}
	d["goal"] = goal
	d["scope"] = c.Scope
	if c.UnitTokens {
		d["idle"] = fmt.Sprintf(c.pick("%d토큰", "%d tokens"), c.IdleTokens)
		d["total"] = fmt.Sprintf(c.pick("%d토큰", "%d tokens"), c.TotalTokens)
	} else {
		d["idle"] = c.won(c.IdleMicro)
		d["total"] = c.won(c.TotalMicro)
	}
	d["waste"] = c.won(v.WasteMicro)
	d["handoff"] = c.Handoff
	kinds, sources := kindNames, sourceNames
	if c.english() {
		kinds, sources = kindNamesEnglish, sourceNamesEnglish
	}
	if k, ok := v.Facts["kind"].(string); ok {
		d["kindname"] = kinds[k]
		if strings.HasPrefix(k, "iron_laws:") {
			d["kindname"] = fmt.Sprintf(c.pick("오철칙 %s 예외 은폐 후보", "candidate for hiding an exception under iron law %s"), strings.TrimPrefix(k, "iron_laws:"))
			if n := toInt(v.Facts["finding_count"]); n > 1 {
				d["kindname"] = fmt.Sprint(d["kindname"]) + fmt.Sprintf(c.pick(" (같은 편집에서 %d개 지적)", " (%d findings in the same edit)"), n)
			}
		}
		if d["kindname"] == "" {
			d["kindname"] = k
		}
	}
	if k, ok := v.Facts["source"].(string); ok {
		if name := sources[k]; name != "" {
			d["sourcename"] = name
		}
	}
	if ft, ok := v.Facts["failed_tests"].([]string); ok {
		d["failed"] = ft
		if len(ft) > 0 {
			d["first_failed"] = ft[0]
		}
	} else if ft, ok := v.Facts["failed_tests"].([]any); ok && len(ft) > 0 {
		d["failed"] = ft
		d["first_failed"] = fmt.Sprint(ft[0])
	}
	if x, ok := v.Facts["per_min_krw"]; ok {
		d["per_min"] = c.apiAmount(toInt(x))
	}
	if x, ok := v.Facts["budget_krw"]; ok {
		d["budget"] = c.Amount(toInt(x))
	}
	if x, ok := v.Facts["spent_krw"]; ok {
		d["spent"] = c.apiAmount(toInt(x))
	}
	if x, ok := v.Facts["idle_krw"]; ok && !c.UnitTokens {
		d["idle"] = c.apiAmount(toInt(x))
	}
	switch v.Facts["kind"] {
	case "failed":
		d["reason"] = c.pick("마지막 검증이 실패한 상태다", "the last verification is failing")
		if cmd, ok := v.Facts["cmd"].(string); ok {
			d["reason"] = fmt.Sprintf(c.pick("마지막 검증 `%s`가 실패한 상태다", "the last verification `%s` is failing"), cmd)
		}
	case "stale":
		d["reason"] = c.pick("마지막 검증 뒤에 코드가 바뀌었고 다시 검증되지 않았다", "the code changed after the last verification and was not verified again")
	case "unverified":
		d["reason"] = c.pick("이 세션에서 검증이 한 번도 실행되지 않았다", "no verification has run in this session")
	case "hollow":
		d["reason"] = fmt.Sprintf(c.pick("바꾼 기능 코드에 TODO·미구현 표시나 상수만 돌려주는 본문이 남아 있다(%v)", "the changed feature code still has TODO or unimplemented markers, or bodies that only return a constant (%v)"), v.Facts["paths"])
	case "no_evidence":
		d["reason"] = fmt.Sprintf(c.pick("요청에 적힌 %v는 어떤 변경에도 나오지 않는다", "%v from the request appears in no change"), v.Facts["missing"])
	case "narrow_check":
		d["reason"] = fmt.Sprintf(c.pick("통과한 검사 `%v`는 이번에 새로 만든 시험 파일만 실행했다", "the passing check `%v` ran only test files created in this work"), v.Facts["cmd"])
	case "unmet":
		d["reason"] = c.pick("계약의 완료 조건 일부가 실패했다", "some completion criteria in the contract failed")
		if u, ok := v.Facts["unmet"].([]string); ok {
			d["unmet"] = strings.Join(u, ", ")
		}
	}
	if u, ok := v.Facts["unmet"].([]string); ok {
		d["unmetlist"] = strings.Join(u, ", ")
	}
	if c.UnitTokens {
		d["waste"] = c.pick("단가 미확인", "price unknown")
	}
	usageUnknown := c.UsageUnknown || (c.TotalMicro == 0 && c.IdleMicro == 0 && !c.UnitTokens)
	if usageUnknown {
		for _, key := range []string{"idle", "total", "waste", "spent", "per_min"} {
			d[key] = c.pick("사용량 미확인", "usage unknown")
		}
	}
	d["usage_unknown"] = usageUnknown
	d["cost_summary"] = fmt.Sprintf(c.pick("계측분 %s, 마지막 진척 이후 %s. 실제 청구액·절감액은 미확인이다.", "Measured %s, %s since the last progress. The actual charge and any saving are unknown."), d["total"], d["idle"])
	if usageUnknown {
		d["cost_summary"] = c.pick("사용량 계측이 없어 금액을 확인할 수 없다.", "No usage was measured, so the amount is unknown.")
	}
	return d
}

func toInt(x any) int64 {
	switch v := x.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
}

func render(l, name string, d map[string]any) string {
	if l != lang.English {
		l = lang.Korean
	}
	for _, suffix := range []string{".agent.tmpl", ".user.tmpl", ".rx.tmpl", ".say.tmpl"} {
		if strings.HasSuffix(name, suffix) && rulepack.IsLearned(strings.TrimSuffix(name, suffix)) {
			name = "learned_repeat" + suffix
			break
		}
	}
	t := tmplsOnce()[l].Lookup(name)
	if t == nil {
		return ""
	}
	var b bytes.Buffer
	if err := t.Execute(&b, d); err != nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(b.String(), "<no value>", ""))
}

// Agent renders the agent-facing nudge. arm selects the experiment arm:
// "fact" omits the prescription line, "none" is the control arm and gets
// the same observation without a prescription.
//
// The control arm used to render nothing at all, so a finding that reached
// the agent carried no message: the pipeline that judges a violation runs
// the same way in every arm, and a message that says nothing is not a
// message. The arms differ by the prescription, not by whether the finding
// is stated.
func Agent(v detect.Signal, c Context, arm string) string {
	d := data(v, c)
	msg := render(c.Lang, v.Rule+".agent.tmpl", d)
	if msg == "" {
		return ""
	}
	if arm != "fact" && arm != "none" {
		if rx := render(c.Lang, v.Rule+".rx.tmpl", d); rx != "" {
			msg += "\n" + rx
		}
	}
	if hint, ok := v.Facts["verification_hint"].(string); ok && arm != "fact" && arm != "none" && hint != "" && strings.HasPrefix(v.Rule, "s1.") {
		msg += "\n" + hint
	}
	return msg
}

// User renders the user-facing message: at most two lines of observation,
// one sentence the user can paste to the agent, then the choices.
func User(v detect.Signal, c Context) string {
	d := data(v, c)
	msg := render(c.Lang, v.Rule+".user.tmpl", d)
	if msg == "" {
		msg = v.Rule
	}
	lines := strings.Split(msg, "\n")
	if len(lines) > 2 {
		lines = lines[:2]
	}
	if say := Say(v, c); say != "" {
		lines = append(lines, c.pick("AI에게 이렇게 말해 보세요: \"", "You can tell the AI: \"")+say+"\"")
	}
	return strings.Join(append(lines, c.pick(Choices, ChoicesEnglish)), "\n")
}

// Say is the one sentence a user can paste to the agent for this finding.
func Say(v detect.Signal, c Context) string {
	return render(c.Lang, v.Rule+".say.tmpl", data(v, c))
}

// Templates lists the template names (for the vocabulary check).
func Templates() []string {
	var out []string
	for _, t := range tmplsOnce()[lang.Korean].Templates() {
		if t.Name() != "" {
			out = append(out, t.Name())
		}
	}
	return out
}

// Raw returns a template's source text.
func Raw(name string) string { return RawIn(lang.Korean, name) }

// RawIn returns a template's source text in the given language.
func RawIn(l, name string) string {
	b, _ := tmplFS.ReadFile("templates/" + l + "/" + name)
	return string(b)
}

// ForbiddenRe matches directive vocabulary that prompt-injection defenses
// react to. Agent messages are observational.
var ForbiddenRe = lazyre.New(`(?i)\bsystem\b|\bIMPORTANT\b|\bmust\b|\bnever\b|\bignore (?:all|previous)\b|하라\b|하십시오|해야 한다|해야만|반드시|절대|즉시 중단|명령한다|지시한다`)
