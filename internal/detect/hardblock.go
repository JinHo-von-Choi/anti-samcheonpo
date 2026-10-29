package detect

// HardBlockRules are the core malicious patterns: an error hidden instead of
// fixed, a test weakened instead of made to pass, a file changed outside the
// agreed scope, and a file rolled back to a state that was already tried.
//
// A finding on one of these rules is not advice. The tool call is refused
// before it runs, whatever the rollout stage says, because the damage these
// patterns cause is in the work itself: a swallowed error, a deleted
// assertion and an out-of-scope edit are already on disk once the call
// returns.
var HardBlockRules = []string{
	"s5.error_hiding",
	"s5.test_weakening",
	"s3.out_of_scope",
	"s2.oscillation",
}

// IsHardBlock reports whether a rule is one of the core malicious patterns
// that must be refused the moment they are found.
//
// The set is deliberately narrow. It covers the patterns where the agent
// changes the evidence rather than the work, and where a later step would
// build on that change without noticing it.
func IsHardBlock(rule string) bool {
	switch rule {
	case "s5.error_hiding", "s5.test_weakening", "s3.out_of_scope", "s2.oscillation":
		return true
	}
	return false
}
