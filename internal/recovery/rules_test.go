package recovery

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var ruleName = regexp.MustCompile(`^s\d+\.`)

// A new rule has to say what its advice is about: the prescription of an
// unknown cause only asks for one more check.
func TestRulesWithAdviceHaveACause(t *testing.T) {
	unknown := map[string]bool{"s4.read_only_streak": true, "s4.serial_triage": true, "s4.research_repeat": true,
		"s5.answer_copy": true, "s9.instruction_smuggling": true, "s9.unverified_claim_request": true, "s10.grader_access": true}
	files, err := filepath.Glob("../intervene/templates/ko/*.agent.tmpl")
	if err != nil || len(files) == 0 {
		t.Fatalf("templates: %v %v", files, err)
	}
	for _, f := range files {
		rule := strings.TrimSuffix(filepath.Base(f), ".agent.tmpl")
		if !ruleName.MatchString(rule) {
			continue // shared message parts, not rules
		}
		if got := Diagnose(rule, "", false); got == Unknown && !unknown[rule] {
			t.Errorf("%s has no cause", rule)
		}
	}
	for rule, want := range map[string]Cause{"s8.leftover_process": Process, "s5.false_done": Verification, "s5.error_hiding": Code, "s5.artifact_meta": Writing, "s8.budget": Budget} {
		if got := Diagnose(rule, "", false); got != want {
			t.Errorf("%s: %s", rule, got)
		}
		if For(Diagnose(rule, "", false)).Action == For(Unknown).Action {
			t.Errorf("%s has the generic prescription", rule)
		}
	}
}
