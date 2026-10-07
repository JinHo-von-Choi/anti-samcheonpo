package intervene

import (
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
)

var rules = []string{"s1.identical_rerun", "s1.verify_after_docs", "s1.review_repeat", "s1.evidence_rerun", "s1.verify_ratio", "s1.test_bloat", "s2.stuck_error", "s2.oscillation", "s2.whack_a_mole", "s2.environment", "s2.attempt_repeat",
	"s3.out_of_scope", "s3.config_bypass", "s3.protected_path", "s4.read_only_streak", "s5.test_weakening", "s5.error_hiding",
	"s5.false_done", "s5.answer_copy", "s7.memory_rot", "s8.velocity", "s8.budget", "s8.idle_spend", "s8.forced_no_progress", "s5.stop_unmet", "s6.capability_limit", "s3.drift"}

// TestVocabulary checks every agent template against the directive word list.
func TestVocabulary(t *testing.T) {
	for _, name := range Templates() {
		if strings.HasSuffix(name, ".user.tmpl") {
			continue
		}
		if loc := ForbiddenRe.FindStringIndex(Raw(name)); loc != nil {
			t.Errorf("%s contains directive vocabulary %q", name, Raw(name)[loc[0]:loc[1]])
		}
	}
}

func TestEveryRuleHasTemplates(t *testing.T) {
	have := map[string]bool{}
	for _, n := range Templates() {
		have[n] = true
	}
	for _, r := range rules {
		for _, kind := range []string{".agent.tmpl", ".user.tmpl", ".rx.tmpl"} {
			if !have[r+kind] {
				t.Errorf("missing %s%s", r, kind)
			}
		}
	}
}

func TestAgentStructureAndArms(t *testing.T) {
	v := detect.Signal{Rule: "s1.identical_rerun", Evidence: []int64{3, 7, 9}, Facts: map[string]any{"cmd": "npm test", "count": 3, "failed_tests": []string{"auth.spec"}}}
	c := Context{Goal: "로그인 만료 처리", IdleMicro: 1_840_000_000}
	fact := Agent(v, c, "fact")
	lines := strings.Split(fact, "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "[삼천포] 관찰:") || !strings.HasPrefix(lines[1], "근거:") ||
		!strings.HasPrefix(lines[2], "비용:") || !strings.HasPrefix(lines[3], "계약:") {
		t.Fatalf("four fixed lines (관찰, 근거, 비용, 계약):\n%s", fact)
	}
	if !strings.Contains(fact, "1,840원") || !strings.Contains(fact, "3, 7, 9") || strings.Contains(fact, "no value") {
		t.Errorf("facts not rendered:\n%s", fact)
	}
	rx := Agent(v, c, "prescription")
	if len(strings.Split(rx, "\n")) != 5 {
		t.Errorf("prescription adds one line:\n%s", rx)
	}
	if Agent(v, c, "none") != "" {
		t.Error("the no-intervention arm sends nothing")
	}
}

func TestUserMessage(t *testing.T) {
	for _, r := range rules {
		v := detect.Signal{Rule: r, Facts: map[string]any{"count": 3, "path": "src/x.ts", "kind": "skip_added", "pct": 80}}
		m := User(v, Context{})
		lines := strings.Split(m, "\n")
		if len(lines) > 3 || lines[len(lines)-1] != Choices {
			t.Errorf("%s: at most three lines ending with choices:\n%s", r, m)
		}
	}
}

func TestUnknownUsageNeverRendersZeroWonInAdvice(t *testing.T) {
	v := detect.Signal{Rule: "s1.identical_rerun", Facts: map[string]any{"count": 3, "cmd": "test"}}
	for _, text := range []string{Agent(v, Context{UsageUnknown: true}, "fact"), User(v, Context{UsageUnknown: true})} {
		if strings.Contains(text, "0원") || !(strings.Contains(text, "미확인") || strings.Contains(text, "확인할 수 없다")) {
			t.Fatal(text)
		}
	}
}
func TestFactArmDoesNotReceiveVerificationPrescription(t *testing.T) {
	v := detect.Signal{Rule: "s1.identical_rerun", Facts: map[string]any{"verification_hint": "specific-stop-prescription"}}
	if strings.Contains(Agent(v, Context{}, "fact"), "specific-stop-prescription") {
		t.Fatal("fact-only comparison arm received a prescription")
	}
	if !strings.Contains(Agent(v, Context{}, "prescription"), "specific-stop-prescription") {
		t.Fatal("prescription lost its evidence hint")
	}
}
