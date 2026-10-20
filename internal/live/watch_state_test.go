package live

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
)

// A lost filesystem watch must reach the session, not stay inside the
// workspace. Before this, the workspace recorded the failure and only the
// agenttime proof path read it, so the monitor reported "available" while
// observing nothing.
func TestWatchFailureReachesSessionAndAssessment(t *testing.T) {
	s, _ := reliabilitySession(t)
	if got := s.watchState(); got != WatchStateOK {
		t.Fatalf("healthy workspace reported state %q", got)
	}
	s.ws.markWatchUnavailable(WatchReasonUnavailable)
	if got := s.watchState(); got != WatchStateUnavailable {
		t.Fatalf("watch failure reported state %q", got)
	}
	if got := s.watchReason(); got != WatchReasonUnavailable {
		t.Fatalf("watch failure reported reason %q", got)
	}
	if got := s.hudState().Assessment.Reasons["watch_unavailable"]; got != 1 {
		t.Fatalf("assessment did not report a lost watch: %v", s.hudState().Assessment.Reasons)
	}
}

// A partial watch is not a total one. The two must stay distinguishable,
// because a degraded watch still updates the fingerprint for the part it
// covers while an unavailable one updates nothing.
func TestWatchDegradedStaysDistinctFromUnavailable(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.ws.markWatchFailure(WatchReasonLimit)
	if got := s.watchState(); got != WatchStateDegraded {
		t.Fatalf("partial watch failure reported state %q", got)
	}
	s.ws.markWatchUnavailable(WatchReasonUnavailable)
	if got := s.watchState(); got != WatchStateUnavailable {
		t.Fatalf("total watch failure reported state %q", got)
	}
}

// A learned rule file that exists but cannot be read is not the same as
// having no learned rules. The difference used to be invisible.
func TestCandidatePackFailureIsRecordedNotSwallowed(t *testing.T) {
	s, _ := reliabilitySession(t)
	home := config.Home()
	if err := os.WriteFile(filepath.Join(home, "learned-rules.json"), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	s.loadCandidatePack()
	if s.candidatePackState != rulepack.LoadUnreadable {
		t.Fatalf("broken rule pack reported state %q", s.candidatePackState)
	}
}

func TestCandidatePackAbsentIsNotAFailure(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.loadCandidatePack()
	if s.candidatePackState != rulepack.LoadAbsent {
		t.Fatalf("missing rule pack reported state %q", s.candidatePackState)
	}
}

func TestCandidatePackInvalidIsRecorded(t *testing.T) {
	s, _ := reliabilitySession(t)
	home := config.Home()
	raw := `{"version":"advisory-rulepack/1","definitions":[],"proposal_digest":"a","evaluation_digest":"b","effectiveness":"validated"}`
	if err := os.WriteFile(filepath.Join(home, "learned-rules.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	s.loadCandidatePack()
	if s.candidatePackState != rulepack.LoadInvalid {
		t.Fatalf("invalid rule pack reported state %q", s.candidatePackState)
	}
}
