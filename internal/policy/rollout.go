package policy

import (
	"encoding/hex"
	"fmt"
)

// RolloutRoute requires an operator-declared evaluation digest for blocking.
// A digest is traceability, not cryptographic proof that a rule is effective.
// No rule is preapproved and this function never promotes one automatically.
func RolloutRoute(mode, rule string, evidence map[string]string) string {
	// Experiment rollout never disables explicit accepted protection/budget.
	if ExplicitGuardrail(rule) {
		return "block"
	}
	if mode == "shadow" {
		return "observe"
	}
	if AdviceOnly(rule) {
		return "advice"
	}
	if mode != "validated" {
		return "advice"
	}
	digest, err := hex.DecodeString(evidence[rule])
	if err != nil || len(digest) != 32 {
		return "advice"
	}
	return "block"
}

// AdviceOnly rules cannot block, even with an operator evaluation digest.
func AdviceOnly(rule string) bool {
	return rule == "s1.explicit_waiting" || rule == "s8.progress_stall"
}

// ExplicitGuardrail reports rules that enforce a limit the user set: a
// protected path, a budget, a session ceiling. They are not experiments.
func ExplicitGuardrail(rule string) bool {
	return rule == "s3.protected_path" || rule == "s8.budget" || rule == "s8.session_ceiling"
}

// Escalate turns advice into a block when the same rule was already advised
// in this session and the agent repeated the behavior. Rules the user marked
// as false positives are never escalated, and blocks per session are capped.
func Escalate(route string, advised, released bool, used, max int) string {
	if route == "advice" && advised && !released && used < max {
		return "block"
	}
	return route
}

// EscalationKey scopes ignored advice to one intent revision, rule, pattern
// and target, so advice about one file never blocks unrelated work.
func EscalationKey(revision uint64, rule, kind, target string) string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%s", revision, rule, kind, target)
}

// Escalable reports whether ignored advice for a rule may become a block.
// Rules backed by observable facts escalate without a contract; rules that
// judge scope, budgets or completion need an accepted contract; estimated
// or model-judged rules and replaced expectations never escalate.
func Escalable(rule, kind string, contractAccepted bool) bool {
	if kind == "literal_replaced" {
		return false
	}
	switch rule {
	case "s1.identical_rerun", "s2.stuck_error", "s2.oscillation", "s2.semantic_oscillation", "s2.environment", "s5.error_hiding", "s5.test_weakening",
		"s1.full_suite_local_change", "s5.release_without_preflight", "s5.release_rate":
		return true
	case "s1.unprobed_long_run":
		// a run time inferred from a test's name is an estimate
		return kind == "explicit"
	case "s2.verifier_deadlock":
		// only rerunning the same input; a turn waiting on the user has
		// nothing left to block
		return kind == "unchanged_rerun"
	case "s5.stop_unmet", "s5.false_done", "s3.out_of_scope", "s3.config_bypass", "s1.verify_ratio", "s2.whack_a_mole", "s1.evidence_rerun":
		return contractAccepted
	default:
		return false
	}
}
