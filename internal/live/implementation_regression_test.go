package live

import (
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
	"strings"
	"testing"
)

func TestRestartRestoresMoreThanTwentyRequests(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "기능 구현해"})
	for i := 0; i < 25; i++ {
		s.onPrompt(HookInput{Prompt: fmt.Sprintf("항목 %d도 추가해", i)})
	}
	if _, err := s.Finalize(); err != nil {
		t.Fatal(err)
	}
	next, err := newSession(s.ID, s.Agent, s.Root, "", s.db, s.prices, adapter.Profiles[s.Agent].Caps)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Finalize()
	if len(next.eng.Requests) != 25 || !strings.Contains(next.eng.RequestText(), "항목 0도 추가해") {
		t.Fatalf("requirements lost: %d", len(next.eng.Requests))
	}
}

func TestObservedCheckPersistsWithoutInventingAuthority(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "로그인 구현해"})
	feedWrite(t, s, "w", "auth/login.go")
	feedCheck(t, s, "c", "go test ./auth", 0)
	events, err := s.db.EventsOf(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Verification == nil {
			continue
		}
		found = true
		o := ev.Verification
		if o.State != "passed" || len(o.RelatedPaths) != 1 || o.Independent || o.CheckID != "" {
			t.Fatalf("observation %+v", o)
		}
	}
	if !found {
		t.Fatal("host check not persisted")
	}
}

func TestCancelledWorkDoesNotHoldNewGoalAfterRestart(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "로그인 구현해"})
	feedWrite(t, s, "old-write", "auth/login.go")
	s.onPrompt(HookInput{Prompt: "작업 취소해"})
	s.onPrompt(HookInput{Prompt: "새 결제 기능 구현해"})
	feedWrite(t, s, "new-write", "payment/pay.go")
	feedCheck(t, s, "new-check", "go test ./payment", 0)
	assert := func(current *Session) {
		t.Helper()
		current.mu.Lock()
		defer current.mu.Unlock()
		if !current.verified() {
			t.Fatal("cancelled goal contaminated new verification scope")
		}
		found := false
		for _, r := range current.requirementGraph().Requirements {
			if r.Label == "로그인 구현해" {
				found = true
				if r.Lifecycle != "cancelled" {
					t.Fatalf("lost cancellation: %+v", r)
				}
			}
		}
		if !found {
			t.Fatal("cancelled requirement disappeared")
		}
	}
	assert(s)
	if _, err := s.Finalize(); err != nil {
		t.Fatal(err)
	}
	next, err := newSession(s.ID, s.Agent, s.Root, "", s.db, s.prices, adapter.Profiles[s.Agent].Caps)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Finalize()
	assert(next)
}

func TestRecoveryNewInputsPermitIndependentProbe(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	zero := 0
	s.eng.St.Events = []*event.Event{{Seq: 1, Kind: event.KindTool, Tool: event.ToolShell, WSBefore: "old", ExecFP: "check", ExitCode: &zero}}
	v := detect.Signal{ID: "old-input", Seq: 1, Rule: "s2.stuck_error", Detector: "S2", Level: detect.L1, Facts: map[string]any{}}
	a, _, ok := s.proposeRecovery(v)
	if !ok {
		t.Fatal("first probe missing")
	}
	s.eng.St.Events = append(s.eng.St.Events, &event.Event{Seq: 2, Kind: event.KindTool, Tool: event.ToolShell, WSBefore: "new", ExecFP: "check", ExitCode: &zero})
	v.Seq = 2
	v.ID = "new-input"
	b, _, ok := s.proposeRecovery(v)
	if !ok || a.CauseKey == b.CauseKey {
		t.Fatal("new observed inputs suppressed")
	}
	if exposed(recovery.Attempt{Arm: "none", PolicyVersion: "intervention-policy/2"}) {
		t.Fatal("undelivered fact treated as exposed")
	}
}
