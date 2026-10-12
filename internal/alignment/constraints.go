package alignment

import (
	"regexp"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

type Constraint struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Name  string `json:"-"`
}

var realRequest = regexp.MustCompile(`(?i)(실제|진짜|실서비스|real|live|production).{0,40}(연동|api|서비스|integration|backend)|(?:연동|integration).{0,30}(실제|real|live)`)
var forbidMock = regexp.MustCompile(`(?i)(목|모의|mock|stub|가짜).{0,24}(금지|쓰지|사용하지|대체하지|말고)|(?:no|without|do not use).{0,20}(mock|stub)`)
var allowMock = regexp.MustCompile(`(?i)(목|모의|mock|stub).{0,24}(허용|사용해|대체해|사용 가능)|(?:use|allow).{0,20}(mock|stub)`)
var negativePrefix = regexp.MustCompile(`(?i)(?:do not|don't|never|not|no)\s*$`)
var noRealIntegration = regexp.MustCompile(`(?i)(실제|진짜|실서비스)[^\n.!?]{0,32}(연동|api|서비스)\s*(?:을|를|은|는)?\s*(?:연동|사용|연결)?\s*(?:을|를)?\s*(하지\s*마|하지\s*말|필요\s*없|사용하지)|(?:do not|don't|no)[^\n.!?]{0,20}(real|live|production)[^\n.!?]{0,20}(api|integration|backend)`)
var preserveChecks = regexp.MustCompile(`(?i)(테스트|검사|assertion|check).{0,40}(삭제하지|약화하지|유지|금지)|(?:keep|preserve).{0,24}(tests?|assertions?|checks?)|do not.{0,20}(delete|weaken).{0,20}(tests?|assertions?)`)
var preserveFeature = regexp.MustCompile(`(?i)(기능|함수|feature|function).{0,30}(유지|삭제하지|제거하지)|(?:keep|preserve).{0,24}(feature|function)`)
var namedFeature = regexp.MustCompile("[\"'`]+([A-Za-z_][A-Za-z0-9_]{1,63})[\"'`]+")

// Constraints come from explicit request text, never a generated agent plan.
// They guide advisory comparisons; they do not grant execution authority.
func Constraints(goal string) []Constraint {
	if len(goal) > 16384 {
		goal = goal[:16384]
	}
	var out []Constraint
	add := func(kind, label, name string) {
		out = append(out, Constraint{ID: "constraint_" + fp.Hash(kind, name, goal)[:12], Kind: kind, Label: label, Name: name})
	}
	allowedMock := false
	for _, span := range allowMock.FindAllStringIndex(goal, -1) {
		prefix := goal[max(0, span[0]-24):span[0]]
		if !negativePrefix.MatchString(prefix) {
			allowedMock = true
		}
	}
	if (realRequest.MatchString(goal) || forbidMock.MatchString(goal)) && !allowedMock && !noRealIntegration.MatchString(goal) {
		add("real_integration", "요청한 실제 연동을 모의 결과로 대체하지 않기", "")
	}
	if preserveChecks.MatchString(goal) {
		add("preserve_checks", "요청한 검사와 검증 조건을 유지하기", "")
	}
	if preserveFeature.MatchString(goal) {
		for _, m := range namedFeature.FindAllStringSubmatch(goal, 4) {
			add("preserve_feature", "요청한 기능 유지: "+strings.TrimSpace(m[1]), m[1])
		}
	}
	return out
}
