package verification

import (
	"testing"
	"time"
)

func TestPlanRequiresFreshIdentityAndKeepsManualSeparate(t *testing.T) {
	now := time.Now()
	e := passingEvidence(now)
	c := Candidate{CheckID: "c", Evidence: &e}
	plan := PlanChecks([]Candidate{c}, now)
	if plan.StopMachineChecks || plan.Items[0].State != "candidate" || plan.Items[0].SourceEventID != e.SourceEventID {
		t.Fatalf("%+v", plan)
	}
	key := e.Key
	c.CurrentKey = &key
	plan = PlanChecks([]Candidate{c, {CheckID: "visual", Manual: true}}, now)
	if !plan.StopMachineChecks || plan.ManualPending != 1 || plan.Items[1].State != "manual" {
		t.Fatalf("%+v", plan)
	}
	key.InputHash = "changed"
	plan = PlanChecks([]Candidate{c}, now)
	if plan.StopMachineChecks || plan.Items[0].State != "invalidated" {
		t.Fatalf("%+v", plan)
	}
	if PlanChecks(nil, now).StopMachineChecks {
		t.Fatal("empty set became completion")
	}
}

func TestPlanNeverRecommendsExpiredOrIncompletePass(t *testing.T) {
	now := time.Now()
	for _, alter := range []func(*Evidence){
		func(e *Evidence) { e.ExpiresAt = now },
		func(e *Evidence) { e.Complete = false },
		func(e *Evidence) { e.Flaky = true },
		func(e *Evidence) { e.SideEffect = true },
		func(e *Evidence) { e.Key.CheckID = "other" },
	} {
		e := passingEvidence(now)
		alter(&e)
		plan := PlanChecks([]Candidate{{CheckID: "c", Evidence: &e, CurrentKey: &e.Key}}, now)
		if plan.StopMachineChecks || plan.Items[0].EvidenceID != "" {
			t.Fatal("invalid pass promoted", plan)
		}
	}
}
