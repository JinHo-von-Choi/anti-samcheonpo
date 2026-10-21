package cli

import (
	"strings"
	"testing"
)

// cleanRound is a round where every request was answered in time.
func cleanRound(n int) benchHookVerdict {
	return benchHookVerdict{
		N: n, Responses: n, FirstOK: true, FirstResponse: true,
		Prompts: 1, ToolCalls: n,
	}
}

// A round without any failure passes and says nothing.
func TestBenchHookCleanRoundPasses(t *testing.T) {
	reason, note := checkBenchHook(cleanRound(600), -1)
	if reason != "" {
		t.Fatalf("reason %q", reason)
	}
	if note != "" {
		t.Fatalf("note %q", note)
	}
}

// A fast failure is the hook that could not do its job. It never passes as
// a latency pass, however few of them there are.
func TestBenchHookFastFailureNeverPasses(t *testing.T) {
	v := cleanRound(600)
	v.FastFailures = 1
	v.Timeouts = 0
	v.NoResponse = 1
	v.Responses = 599
	reason, _ := checkBenchHook(v, -1)
	if reason == "" {
		t.Fatal("a fast failure passed")
	}
	if !strings.Contains(reason, "지연 통과로 인정하지 않는다") {
		t.Fatalf("reason %q", reason)
	}
}

// A timeout is the hook that was slow. An isolated one is tolerated, and
// the pass says so instead of staying silent.
func TestBenchHookToleratesIsolatedTimeout(t *testing.T) {
	v := cleanRound(600)
	v.Timeouts = 1
	v.NoResponse = 1
	v.Responses = 599
	reason, note := checkBenchHook(v, -1)
	if reason != "" {
		t.Fatalf("reason %q", reason)
	}
	if !strings.Contains(note, "느린 기동 1회") {
		t.Fatalf("note %q", note)
	}
}

// A systematic slowdown is not tolerated.
func TestBenchHookRejectsTimeoutsOverTolerance(t *testing.T) {
	v := cleanRound(600)
	v.Timeouts = 7
	v.NoResponse = 7
	v.Responses = 593
	reason, _ := checkBenchHook(v, -1)
	if reason == "" {
		t.Fatal("too many timeouts passed")
	}
	if !strings.Contains(reason, "허용 한도") {
		t.Fatalf("reason %q", reason)
	}
}

// A missing answer that was not a timeout is not explained by a timeout.
func TestBenchHookRejectsMissingResponseWithoutTimeout(t *testing.T) {
	v := cleanRound(600)
	v.NoResponse = 1
	v.Responses = 599
	reason, _ := checkBenchHook(v, -1)
	if reason == "" {
		t.Fatal("a missing response passed")
	}
}

// The daemon's own counts stay absolute.
func TestBenchHookRejectsDaemonCountMismatch(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*benchHookVerdict)
	}{
		{"prompts", func(v *benchHookVerdict) { v.Prompts = 0 }},
		{"tool calls", func(v *benchHookVerdict) { v.ToolCalls = 599 }},
		{"queue rejected", func(v *benchHookVerdict) { v.QueueRejected = 1 }},
	}
	for _, c := range cases {
		v := cleanRound(600)
		c.mut(&v)
		if reason, _ := checkBenchHook(v, -1); reason == "" {
			t.Fatalf("%s mismatch passed", c.name)
		}
	}
}

// The cold start may outrun its own deadline while the daemon boots. That
// is the designed behaviour and does not fail the round.
func TestBenchHookColdStartTimeoutIsDesignedBehaviour(t *testing.T) {
	v := cleanRound(600)
	v.FirstOK = false
	v.FirstResponse = false
	v.ColdLate = true
	if reason, _ := checkBenchHook(v, -1); reason != "" {
		t.Fatalf("reason %q", reason)
	}
	// Without the cold-start exemption the same round fails.
	v.ColdLate = false
	if reason, _ := checkBenchHook(v, -1); reason == "" {
		t.Fatal("a missing cold start passed")
	}
}

// The tolerance is one percent of the rounds and never less than one,
// unless the caller states it.
func TestBenchHookTolerance(t *testing.T) {
	cases := []struct {
		n, max, want int
	}{
		{600, -1, 6},
		{200, -1, 2},
		{50, -1, 1},
		{1, -1, 1},
		{600, 0, 0},
		{600, 3, 3},
	}
	for _, c := range cases {
		if got := benchHookTolerance(c.n, c.max); got != c.want {
			t.Fatalf("tolerance(%d,%d)=%d want %d", c.n, c.max, got, c.want)
		}
	}
}

// A stated tolerance of zero means no slow round is tolerated at all.
func TestBenchHookZeroToleranceRejectsSingleTimeout(t *testing.T) {
	v := cleanRound(600)
	v.Timeouts = 1
	v.NoResponse = 1
	v.Responses = 599
	if reason, _ := checkBenchHook(v, 0); reason == "" {
		t.Fatal("a timeout passed with zero tolerance")
	}
}
