package handoff

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
)

func testBundle(t *testing.T) Bundle {
	t.Helper()
	now := time.Now().UTC()
	task, err := intent.NewTask("project", now)
	if err != nil {
		t.Fatal(err)
	}
	c := &contract.Contract{Goal: "finish", Done: []contract.Check{{ID: "check", Check: "true"}, {ID: "manual", Manual: "look"}}}
	r, err := intent.Revise(task.ID, nil, c.Goal, intent.Source{Origin: intent.User, SessionID: "source", EventID: "request", At: now})
	if err != nil {
		t.Fatal(err)
	}
	r.ContractHash = c.ChecksHash()
	e := verification.Evidence{Version: verification.Version, ID: "proof", Key: verification.Key{TaskID: task.ID, Revision: 1, CheckID: "check", CommandHash: "command", InputHash: "input", EnvironmentHash: "env", RunnerVersion: "runner"}, SessionID: "source", SourceEventID: "check-result", ObservedAt: now, ExpiresAt: now.Add(time.Hour), Pass: true, Complete: true, ResultHash: "result"}
	return Bundle{Version: Version, CreatedAt: now, Source: SessionRef{Agent: "claude", ID: "source"}, Task: &task, Revisions: []intent.Revision{r}, Contract: c, Evidence: []verification.Evidence{e}}
}

func TestHandoffCannotInheritPassWithoutCurrentEnvironment(t *testing.T) {
	b := testBundle(t)
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	plan := decoded.VerificationPlan(nil, time.Now())
	if plan.StopMachineChecks || plan.Items[0].State != "candidate" || plan.ManualPending != 1 {
		t.Fatalf("%+v", plan)
	}
	key := b.Evidence[0].Key
	key.EnvironmentHash = "other environment"
	plan = decoded.VerificationPlan(map[string]verification.Key{"check": key}, time.Now())
	if plan.StopMachineChecks || plan.Items[0].State != "invalidated" {
		t.Fatalf("%+v", plan)
	}
	key = b.Evidence[0].Key
	plan = decoded.VerificationPlan(map[string]verification.Key{"check": key}, time.Now())
	if !plan.StopMachineChecks || plan.ManualPending != 1 {
		t.Fatalf("%+v", plan)
	}
	decoded.ObservationGap = 1
	if decoded.VerificationPlan(map[string]verification.Key{"check": key}, time.Now()).StopMachineChecks {
		t.Fatal("gap ignored")
	}
	if text := decoded.Text(nil, time.Now()); !strings.Contains(text, "승인이 아닙니다") || !strings.Contains(text, "관측 누락") {
		t.Fatal(text)
	}
}

func TestHandoffRejectsBrokenIdentityAndUnknownProtocol(t *testing.T) {
	for _, mutate := range []func(*Bundle){
		func(b *Bundle) { b.Version = "future" },
		func(b *Bundle) { b.Task = nil },
		func(b *Bundle) { b.Revisions[0].Number = 2 },
		func(b *Bundle) { b.Evidence[0].Key.TaskID = "other" },
		func(b *Bundle) { b.Contract.Goal = "changed" },
	} {
		b := testBundle(t)
		mutate(&b)
		if err := b.Validate(); err == nil {
			t.Fatal("broken bundle accepted")
		}
	}
	b := testBundle(t)
	raw, _ := json.Marshal(b)
	if _, err := Decode(strings.NewReader(string(raw) + ` {}`)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, err := Decode(strings.NewReader(strings.Repeat(" ", MaxBytes+1))); err == nil {
		t.Fatal("oversized bundle accepted")
	}
}
