// Package intervene renders agent nudges and user messages from verdicts.
package intervene

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"sync"
	"text/template"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
)

//go:embed templates/ko/*.tmpl
var tmplFS embed.FS

var tmplsOnce = sync.OnceValue(func() *template.Template {
	return template.Must(template.New("").Option("missingkey=zero").ParseFS(tmplFS, "templates/ko/*.tmpl"))
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
}

// Choices is the fixed user choice line.
const Choices = "[/samcheonpo:keep now 이번만 허용]  [/samcheonpo:keep normal 이 판정은 틀림]  [/samcheonpo:steer 방향 바꾸라고 하기]  [/samcheonpo:summary 멈추고 요약 받기]"

func RecoveryMarker(id string) string { return "[sc-recovery:" + id + "]" }

func Recovery(a recovery.Attempt) string {
	return render("recovery.agent.tmpl", map[string]any{"marker": RecoveryMarker(a.ID), "action": a.Prescription.Action, "stop": a.Prescription.StopCondition})
}

func won(micro int64) string {
	return "API 환산 " + contract.Comma((micro+500_000)/1_000_000) + "원"
}

var kindNames = map[string]string{
	"test_deleted": "시험 파일 삭제", "test_removed": "시험 함수 삭제", "skip_added": "skip 표시 추가",
	"assertion_weakened": "단언 약화", "literal_replaced": "기대값 교체", "ignore_added": "무시 주석",
	"empty_catch": "빈 예외 처리", "or_true": "`|| true`로 실패 무시", "test_infra_changed": "시험 실행 설정 변경",
	"assertion_mutilation": "단언문 삭제·약화·skip 주입",
	"dependency":           "의존성 파일", "config": "검사·빌드 설정 파일",
	"unresolvable_host": "접속 주소를 찾을 수 없음", "service_down": "접속할 서비스가 꺼져 있음", "dependency_missing": "설치되지 않은 의존성",
	"permission": "권한 없음", "transient": "일시적인 네트워크 장애",
	"mock_substituted": "시험의 실제 외부 서비스를 가짜로 대체", "stale_goal": "이전 목표의 경로로 되돌아감", "guessed": "요청에서 추정한 범위 밖",
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
	goal := c.Goal
	if goal == "" {
		goal = "첫 요청에 적힌 작업"
	}
	d["goal"] = goal
	d["scope"] = c.Scope
	if c.UnitTokens {
		d["idle"] = fmt.Sprintf("%d토큰", c.IdleTokens)
		d["total"] = fmt.Sprintf("%d토큰", c.TotalTokens)
	} else {
		d["idle"] = won(c.IdleMicro)
		d["total"] = won(c.TotalMicro)
	}
	d["waste"] = won(v.WasteMicro)
	d["handoff"] = c.Handoff
	if k, ok := v.Facts["kind"].(string); ok {
		d["kindname"] = kindNames[k]
		if strings.HasPrefix(k, "iron_laws:") {
			d["kindname"] = "오철칙 " + strings.TrimPrefix(k, "iron_laws:") + " 예외 은폐"
		}
		if d["kindname"] == "" {
			d["kindname"] = k
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
		d["per_min"] = "API 환산 " + contract.Comma(toInt(x)) + "원"
	}
	if x, ok := v.Facts["budget_krw"]; ok {
		d["budget"] = contract.Comma(toInt(x)) + "원"
	}
	if x, ok := v.Facts["spent_krw"]; ok {
		d["spent"] = "API 환산 " + contract.Comma(toInt(x)) + "원"
	}
	if x, ok := v.Facts["idle_krw"]; ok && !c.UnitTokens {
		d["idle"] = "API 환산 " + contract.Comma(toInt(x)) + "원"
	}
	switch v.Facts["kind"] {
	case "failed":
		d["reason"] = "마지막 검증이 실패한 상태다"
		if cmd, ok := v.Facts["cmd"].(string); ok {
			d["reason"] = fmt.Sprintf("마지막 검증 `%s`가 실패한 상태다", cmd)
		}
	case "stale":
		d["reason"] = "마지막 검증 뒤에 코드가 바뀌었고 다시 검증되지 않았다"
	case "unverified":
		d["reason"] = "이 세션에서 검증이 한 번도 실행되지 않았다"
	case "unmet":
		d["reason"] = "계약의 완료 조건 일부가 실패했다"
		if u, ok := v.Facts["unmet"].([]string); ok {
			d["unmet"] = strings.Join(u, ", ")
		}
	}
	if u, ok := v.Facts["unmet"].([]string); ok {
		d["unmetlist"] = strings.Join(u, ", ")
	}
	if c.UnitTokens {
		d["waste"] = "단가 미확인"
	}
	usageUnknown := c.UsageUnknown || (c.TotalMicro == 0 && c.IdleMicro == 0 && !c.UnitTokens)
	if usageUnknown {
		for _, key := range []string{"idle", "total", "waste", "spent", "per_min"} {
			d[key] = "사용량 미확인"
		}
	}
	d["usage_unknown"] = usageUnknown
	d["cost_summary"] = fmt.Sprintf("계측분 %s, 마지막 진척 이후 %s. 실제 청구액·절감액은 미확인이다.", d["total"], d["idle"])
	if usageUnknown {
		d["cost_summary"] = "사용량 계측이 없어 금액을 확인할 수 없다."
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

func render(name string, d map[string]any) string {
	t := tmplsOnce().Lookup(name)
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
// "none" returns "" (logged only), "fact" omits the prescription line.
func Agent(v detect.Signal, c Context, arm string) string {
	if arm == "none" {
		return ""
	}
	d := data(v, c)
	msg := render(v.Rule+".agent.tmpl", d)
	if msg == "" {
		return ""
	}
	if arm != "fact" {
		if rx := render(v.Rule+".rx.tmpl", d); rx != "" {
			msg += "\n" + rx
		}
	}
	if hint, ok := v.Facts["verification_hint"].(string); ok && arm != "fact" && hint != "" && strings.HasPrefix(v.Rule, "s1.") {
		msg += "\n" + hint
	}
	return msg
}

// User renders the user-facing message: at most two lines of observation,
// one sentence the user can paste to the agent, then the choices.
func User(v detect.Signal, c Context) string {
	d := data(v, c)
	msg := render(v.Rule+".user.tmpl", d)
	if msg == "" {
		msg = v.Rule
	}
	lines := strings.Split(msg, "\n")
	if len(lines) > 2 {
		lines = lines[:2]
	}
	if say := Say(v, c); say != "" {
		lines = append(lines, "AI에게 이렇게 말해 보세요: \""+say+"\"")
	}
	return strings.Join(append(lines, Choices), "\n")
}

// Say is the one sentence a user can paste to the agent for this finding.
func Say(v detect.Signal, c Context) string {
	return render(v.Rule+".say.tmpl", data(v, c))
}

// Templates lists the template names (for the vocabulary check).
func Templates() []string {
	var out []string
	for _, t := range tmplsOnce().Templates() {
		if t.Name() != "" {
			out = append(out, t.Name())
		}
	}
	return out
}

// Raw returns a template's source text.
func Raw(name string) string {
	b, _ := tmplFS.ReadFile("templates/ko/" + name)
	return string(b)
}

// ForbiddenRe matches directive vocabulary that prompt-injection defenses
// react to. Agent messages are observational.
var ForbiddenRe = lazyre.New(`(?i)\bsystem\b|\bIMPORTANT\b|\bmust\b|\bnever\b|\bignore (?:all|previous)\b|하라\b|하십시오|해야 한다|해야만|반드시|절대|즉시 중단|명령한다|지시한다`)
