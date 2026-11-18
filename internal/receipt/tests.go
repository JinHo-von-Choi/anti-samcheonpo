package receipt

import (
	"fmt"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
)

// BaselineLine is one baseline comparison of a passing test command.
type BaselineLine struct {
	Outcome string
	Tests   []string
	Failed  []string
	Reason  string
}

// TestInput is what the test changes section is built from.
type TestInput struct {
	Files     []detect.TestFileChange
	Baselines []BaselineLine
	Trivial   []string
	Verdicts  []detect.Signal
	Mutants   []MutantLine
}

// MutantLine is what one mutation tool found on the changed lines.
type MutantLine struct {
	Tool      string
	Ran       bool
	Total     int
	Missed    int
	Survivors []string
	Note      string
}

// maxSurvivorsShown caps the surviving mutants listed per tool.
const maxSurvivorsShown = 10

// MutantSentences describes mutation runs on the changed lines.
func MutantSentences(ms []MutantLine) []string {
	var out []string
	ran := false
	for _, m := range ms {
		ran = ran || m.Ran
		if !m.Ran {
			out = append(out, fmt.Sprintf(lang.T("변이 시험(%s)을 실행하지 못했다: %s."), m.Tool, m.Note))
			continue
		}
		s := fmt.Sprintf(lang.T("변이 시험(%s): 바뀐 줄의 변이 %d개 중 시험이 잡지 못한 변이 %d개."), m.Tool, m.Total, m.Missed)
		if len(m.Survivors) > 0 {
			shown := m.Survivors
			if len(shown) > maxSurvivorsShown {
				shown = shown[:maxSurvivorsShown]
			}
			s += lang.T(" 살아남은 변이: ") + strings.Join(shown, "; ")
			if n := len(m.Survivors) - len(shown); n > 0 {
				s += fmt.Sprintf(lang.T(" 외 %d개"), n)
			}
			s += "."
		}
		if m.Note != "" {
			s += " " + m.Note + "."
		}
		out = append(out, s)
	}
	if ran {
		out = append(out, lang.T("변이 시험은 바뀐 줄만 다루므로 다른 곳의 변경으로 생긴 시험 공백은 찾지 못한다. 전체 변이 실행을 대신하지 않는다."))
	}
	return out
}

// testRules are the findings about tests shown in the section.
var testRules = map[string]bool{"s5.test_weakening": true, "s5.answer_copy": true, "s5.test_edit_to_pass": true,
	"s5.subject_mocked": true, "s5.test_special_case": true, "s5.snapshot_regenerated": true}

// TestSection describes, in plain sentences, what the session did to the
// tests and what the tests that passed show. The receipt and the summary put
// it first: a passing test is only worth what it checks.
func TestSection(in TestInput) []string {
	var findings []string
	seen := map[string]bool{}
	for _, v := range in.Verdicts {
		if v.Level < detect.L1 || v.Suppressed || !(testRules[v.Rule] || v.Rule == detect.RuleFalseDone && v.Facts["kind"] == "baseline_pass") {
			continue
		}
		d := Describe(v)
		if !seen[d] {
			seen[d] = true
			findings = append(findings, d)
		}
	}
	if len(in.Files) == 0 && len(in.Baselines) == 0 && len(findings) == 0 {
		return append([]string{lang.T("시험 변경 없음")}, MutantSentences(in.Mutants)...)
	}
	var out []string
	if n := len(in.Files); n > 0 {
		created, deleted, plus, minus, skips := 0, 0, 0, 0, 0
		var mocks []string
		for _, f := range in.Files {
			switch {
			case f.Deleted:
				deleted++
			case f.Created:
				created++
			}
			if f.AssertDelta > 0 {
				plus += f.AssertDelta
			} else {
				minus -= f.AssertDelta
			}
			skips += f.SkipsAdded
			for _, m := range f.Mocks {
				if len(mocks) < 5 && !contains(mocks, m) {
					mocks = append(mocks, m)
				}
			}
		}
		line := fmt.Sprintf(lang.T("바뀐 시험 파일 %d개(새 파일 %d개, 지운 파일 %d개), 단언 %d개 늘고 %d개 줄었다"), n, created, deleted, plus, minus)
		if skips > 0 {
			line += fmt.Sprintf(lang.T(", 새 skip %d개"), skips)
		}
		if len(mocks) > 0 {
			line += lang.T(", mock 대상: ") + strings.Join(mocks, ", ")
		}
		out = append(out, line+".")
	}
	for _, b := range in.Baselines {
		out = append(out, baselineSentence(b))
	}
	out = append(out, MutantSentences(in.Mutants)...)
	if len(in.Baselines) == 0 {
		for _, t := range in.Trivial {
			out = append(out, lang.T("확인하는 것이 없는 새 시험: ")+trivialText(t))
		}
	}
	if len(findings) == 0 {
		out = append(out, lang.T("시험 약화 판정 없음"))
	}
	return append(out, findings...)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// trivialText turns "path: name: kind" into a sentence part.
func trivialText(t string) string {
	i := strings.LastIndex(t, ": ")
	if i < 0 {
		return t
	}
	kind := map[string]string{"no_assertion": lang.T("단언 없음"), "mock_calls_only": lang.T("mock 호출 여부만 확인"), "self_compared": lang.T("검증 대상끼리 비교")}[t[i+2:]]
	if kind == "" {
		return t
	}
	return t[:i] + " (" + kind + ")"
}

func baselineSentence(b BaselineLine) string {
	tests := "`" + strings.Join(b.Tests, "`, `") + "`"
	switch b.Outcome {
	case "verified":
		s := fmt.Sprintf(lang.T("시험 %s는 변경 전 코드에서는 실패하고 지금은 통과한다. 이번 변경을 확인하는 시험이다."), tests)
		if len(b.Failed) > 0 {
			s += fmt.Sprintf(lang.T(" (변경 전 실패: %s)"), strings.Join(b.Failed, ", "))
		}
		return s
	case "baseline_pass":
		return fmt.Sprintf(lang.T("시험 %s는 변경 전 코드에서도 통과한다. 이 시험으로는 이번 변경을 확인할 수 없다."), tests)
	case "behavior_preserved":
		return fmt.Sprintf(lang.T("시험 %s는 변경 전과 후 모두 통과한다. 동작을 유지하는 변경이라 맞는 결과다."), tests)
	case "build_failure":
		return fmt.Sprintf(lang.T("시험 %s는 변경 전 코드에서는 빌드나 수집 단계에서 멈춘다. 새 코드가 있는지만 확인했고 동작은 확인하지 않았다."), tests)
	}
	reasons := map[string]string{
		"environment":                   lang.T("변경 전 코드에서 의존성·권한·네트워크 같은 코드 밖 원인으로 실패"),
		"undetermined":                  lang.T("변경 전 코드에서 실패했지만 이번에 바꾼 시험의 실패는 아님"),
		"timeout":                       lang.T("시간 제한 초과"),
		"time_mismatch":                 lang.T("통과한 뒤 파일이 바뀌어 통과한 상태를 다시 만들 수 없음"),
		"snapshot_failed":               lang.T("작업 폴더 상태를 기록하지 못함"),
		"diff_failed":                   lang.T("세션 전후 비교에 실패"),
		"baseline_failed":               lang.T("변경 전 상태를 만들지 못함"),
		"worktree_failed":               lang.T("분리 작업 트리를 만들지 못함"),
		"network_isolation_unavailable": lang.T("네트워크를 분리할 수 없어 실행하지 않음"),
		"agent_check_running":           lang.T("같은 작업 폴더에서 다른 무거운 검사가 돌고 있어 실행하지 않음"),
		"agent_check_started":           lang.T("에이전트가 무거운 검사를 시작해 대조를 멈춤"),
		"daemon_stopped":                lang.T("삼천포가 종료되어 대조를 멈춤"),
		"not_git_repository":            lang.T("git 저장소가 아니라서 변경 전 상태를 만들 수 없음"),
		"start_not_recorded":            lang.T("세션 시작 상태를 기록하는 동안 파일이 바뀌어 변경 전 상태를 알 수 없음"),
	}
	reason := reasons[b.Outcome]
	if reason == "" {
		reason = reasons[b.Reason]
	}
	if reason == "" {
		reason = b.Reason
	}
	return fmt.Sprintf(lang.T("시험 %s의 변경 전 대조를 하지 못했다: %s."), tests, reason)
}
