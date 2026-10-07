package live

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
)

func proposeTestRecovery(t *testing.T, s *Session) recovery.Attempt {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	v := detect.Signal{ID: "recovery-verdict", Seq: 1, Rule: "s2.stuck_error", Detector: "S2", Level: detect.L1, Primary: true, Facts: map[string]any{"count": 4, "cmd": "test"}}
	s.deliver([]detect.Signal{v})
	if len(s.recoveries) != 1 {
		t.Fatalf("no proposal: %+v", s.recoveries)
	}
	if s.storageErr != nil {
		t.Fatal(s.storageErr)
	}
	return s.recoveries[0]
}

func TestRecoveryProposalIsNotDeliveryAndDuplicateIsSuppressed(t *testing.T) {
	s, _ := reliabilitySession(t)
	a := proposeTestRecovery(t, s)
	if a.Stage != recovery.Proposed || a.DeliveredAt != nil {
		t.Fatal("queue became delivery")
	}
	s.mu.Lock()
	s.deliver([]detect.Signal{{ID: "again", Seq: 2, Rule: a.Rule, Detector: "S2", Level: detect.L1, Primary: true, Facts: map[string]any{}}})
	count := len(s.recoveries)
	pending := len(s.pending)
	s.mu.Unlock()
	if count != 1 || pending != 1 {
		t.Fatalf("repeated prescription: %d/%d", count, pending)
	}
	var delivered int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM intervention WHERE delivered_at IS NOT NULL`).Scan(&delivered); err != nil || delivered != 0 {
		t.Fatalf("false delivered record %d %v", delivered, err)
	}
}

func TestRecoveryUnknownCapabilityDoesNotClaimDelivery(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.caps = adapter.ObserveOnly
	a := proposeTestRecovery(t, s)
	if a.Stage != recovery.Censored || a.Observation != "unsupported_delivery" || a.DeliveredAt != nil {
		t.Fatalf("%+v", a)
	}
	if len(s.pending) != 0 || len(s.userMsg) != 0 {
		t.Fatal("unsupported prescription queued")
	}
}

func TestRecoveryReceiptRequiresMatchingPrintedAcknowledgment(t *testing.T) {
	for _, mode := range []string{"printed", "wrong-token", "not-printed", "old-client"} {
		t.Run(mode, func(t *testing.T) {
			s, d := reliabilitySession(t)
			a := proposeTestRecovery(t, s)
			client, server := net.Pipe()
			done := make(chan struct{})
			go func() { defer close(done); d.serve(server) }()
			client.SetDeadline(time.Now().Add(3 * time.Second))
			payload, _ := json.Marshal(HookInput{SessionID: s.ID, Cwd: s.Root, Prompt: "continue"})
			request, _ := json.Marshal(Request{V: 1, Agent: "claude", Event: "UserPromptSubmit", Payload: payload})
			if _, err := client.Write(append(request, '\n')); err != nil {
				t.Fatal(err)
			}
			line, err := bufio.NewReader(client).ReadBytes('\n')
			if err != nil {
				t.Fatal(err)
			}
			var response Response
			if err = json.Unmarshal(line, &response); err != nil {
				t.Fatal(err)
			}
			if response.Error != "" || response.DeliveryToken == "" || !strings.Contains(string(response.Output), recoveryMarker(a.ID)) {
				t.Fatalf("%+v", response)
			}
			if mode != "old-client" {
				token := response.DeliveryToken
				if mode == "wrong-token" {
					token = "wrong"
				}
				ack, _ := json.Marshal(map[string]any{"delivery_token": token, "printed": mode != "not-printed"})
				if _, err = client.Write(append(ack, '\n')); err != nil {
					t.Fatal(err)
				}
			}
			client.Close()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("receipt handler stuck")
			}
			s.mu.Lock()
			got := s.recoveries[0]
			s.mu.Unlock()
			want := recovery.Emitted
			if mode == "printed" {
				want = recovery.Delivered
			}
			if got.Stage != want {
				t.Fatalf("stage %s want %s", got.Stage, want)
			}
			persisted, err := s.db.Recoveries(s.Agent, s.ID)
			if err != nil || len(persisted) != 1 || persisted[0].Stage != want {
				t.Fatalf("%+v %v", persisted, err)
			}
		})
	}
}

func TestRecoveryEffectWindowStartsAfterDeliveryAndDoesNotClaimResolved(t *testing.T) {
	s, _ := reliabilitySession(t)
	a := proposeTestRecovery(t, s)
	out := json.RawMessage(`{"text":"` + recoveryMarker(a.ID) + `"}`)
	s.mu.Lock()
	s.eng.St.Events = append(s.eng.St.Events, &event.Event{Seq: 30})
	s.mu.Unlock()
	s.markRecoveryOutput(out, recovery.Emitted)
	s.markRecoveryOutput(out, recovery.Delivered)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trackOutcomes(&event.Event{Seq: 31})
	if s.recoveries[0].Stage != recovery.Delivered {
		t.Fatal("proposal-time window credited as post-delivery observation")
	}
	s.trackOutcomes(&event.Event{Seq: 40})
	got := s.recoveries[0]
	if got.Stage != recovery.EffectObserved || got.Observation != "no_recurrence_observed" {
		t.Fatalf("%+v", got)
	}
	if got.Observation == "resolved" {
		t.Fatal("absence of warnings proved resolution")
	}
}

func TestRecoveryFinalizationPreservesUnconfirmedEffect(t *testing.T) {
	s, _ := reliabilitySession(t)
	proposeTestRecovery(t, s)
	if _, err := s.Finalize(); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Recoveries(s.Agent, s.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("%+v %v", rows, err)
	}
	if rows[0].Stage != recovery.Censored || rows[0].Observation != "session_ended_before_effect_observation" {
		t.Fatalf("%+v", rows[0])
	}
}

func TestEnvironmentSummaryDoesNotSendUserIntoAnotherCodeLoop(t *testing.T) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "true")
	s.mu.Lock()
	s.eng.St.Events = append(s.eng.St.Events, &event.Event{Seq: 1, IsError: true, Text: "Permission denied", ErrFPs: []string{"permission"}})
	s.mu.Unlock()
	a := proposeTestRecovery(t, s)
	if a.Prescription.Cause != recovery.Environment {
		t.Fatal(a.Prescription)
	}
	summary := s.plainSummary()
	if strings.Contains(summary, "통과하도록 고쳐") || !strings.Contains(summary, "외부 조건 변경") {
		t.Fatal(summary)
	}
}
