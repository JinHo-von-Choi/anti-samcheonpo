package recovery

import (
	"testing"
	"time"
)

func TestEnvironmentDoesNotPrescribeMoreCodeChanges(t *testing.T) {
	if Diagnose("s2.stuck_error", "Permission denied", false) != Code {
		t.Fatal("prose became environment evidence")
	}
	cause := Diagnose("s2.stuck_error", "Permission denied", true)
	p := For(cause)
	if cause != Environment || !p.Handoff || p.MaxAttempts != 1 || p.StopCondition == "" {
		t.Fatalf("%+v", p)
	}
}

func TestBoundedRecoveryAndUnknownDelivery(t *testing.T) {
	now := time.Now()
	a := Attempt{Version: "recovery/1", ID: "a", Agent: "claude", SessionID: "s", Revision: 1, CauseKey: "error", VerdictID: "v", Prescription: For(Code), CreatedAt: now, Stage: Proposed}
	if _, why := Select(Code, "error", "", 1, []Attempt{a}); why != RefusedOpen {
		t.Fatal("repeated pending prescription")
	}
	if err := a.Advance(Delivered, now); err == nil {
		t.Fatal("proposal skipped emission")
	}
	if err := a.Advance(Emitted, now); err != nil {
		t.Fatal(err)
	}
	a.Observation = "no_recurrence_observed"
	if err := a.Advance(EffectObserved, now); err == nil {
		t.Fatal("unknown delivery claimed effect")
	}
	if err := a.Advance(Delivered, now); err != nil {
		t.Fatal(err)
	}
	if err := a.Advance(EffectObserved, now); err != nil {
		t.Fatal(err)
	}
	p, why := Select(Code, "error", "", 1, []Attempt{a})
	if why != "" || !p.Handoff {
		t.Fatal("failed approach repeated instead of handoff")
	}
	b := a
	b.ID = "b"
	b.Prescription = p
	b.Stage = Censored
	if _, why := Select(Code, "error", "", 1, []Attempt{a, b}); why != RefusedHandoff {
		t.Fatal("handoff loop")
	}
	if _, why := Select(Code, "new", "", 1, []Attempt{a, b, a}); why != RefusedBudget {
		t.Fatal("revision limit exceeded")
	}
	// a rule that used its budget does not silence another rule
	if _, why := Select(Code, "new", "s3.out_of_scope", 1, []Attempt{a, b, a}); why != "" {
		t.Fatalf("another rule's first finding was refused: %s", why)
	}
	if _, why := Select(Code, "error", "", 2, []Attempt{a, b}); why != "" {
		t.Fatal("explicit revision cannot start new attempt")
	}
}

func TestDiagnoseExternalFailureClasses(t *testing.T) {
	for _, text := range []string{"Error: Cannot find module 'yaml'", "URLError: <urlopen error [Errno -2] Name or service not known>", "connect ECONNREFUSED 127.0.0.1:5432"} {
		if Diagnose("s2.stuck_error", text, true) != Environment {
			t.Errorf("%q is an environment cause", text)
		}
	}
	if Diagnose("s2.stuck_error", "Error: Cannot find module './util'", true) != Code {
		t.Error("a relative import is a code cause")
	}
	if Diagnose("s2.stuck_error", "Cannot find module 'yaml'", false) != Code {
		t.Error("prose without a failed run is not evidence")
	}
	if Diagnose("s2.environment", "", false) != Environment {
		t.Error("the environment rule maps to the environment cause")
	}
}
