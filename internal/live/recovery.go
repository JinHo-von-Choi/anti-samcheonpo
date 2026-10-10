package live

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intervene"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/policy"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
)

func recoveryMarker(id string) string { return intervene.RecoveryMarker(id) }

// proposeRecovery is called under s.mu. It reserves a bounded proposal before
// queueing, so repeated warnings cannot create a self-perpetuating nudge loop.
func (s *Session) proposeRecovery(v detect.Signal) (recovery.Attempt, string, bool) {
	var target *event.Event
	var failed bool
	var errorText string
	var signatures []string
	exit := 0
	for i := len(s.eng.St.Events) - 1; i >= 0; i-- {
		ev := s.eng.St.Events[i]
		if ev.Seq > v.Seq {
			continue
		}
		target = ev
		failed = ev.IsError || ev.ExitCode != nil && *ev.ExitCode != 0
		errorText = ev.Text
		signatures = ev.ErrFPs
		if failed {
			exit = fp.ExitUnknown
			if ev.ExitCode != nil && *ev.ExitCode != 0 {
				exit = *ev.ExitCode
			}
		}
		break
	}
	// prose without a failed run is not evidence: exit 0 reads no cause from it
	res, cause := recovery.DiagnoseExecution(v.Rule, exit, errorText)
	key := fp.Hash(string(cause), v.Rule, strings.Join(signatures, "|"))
	p, ok := recovery.Select(cause, key, s.contractRevision, s.recoveries)
	if !ok {
		return recovery.Attempt{}, "", false
	}
	if res.Category == recovery.CategoryExternalEnvironment {
		p.Shell = res.PrescribedAction
	}
	if v.Level == detect.L1 && v.Arm == "fact" {
		p.Action = "관측 사실만 전달한다(처방 없는 비교군)."
		p.StopCondition = "자동 복구 처방을 추가하지 않는다."
		p.MaxAttempts = 0
		p.Handoff = false
	}
	decision := policy.Decide("advice", policy.Capabilities{Inject: s.caps.InjectPre || s.caps.InjectPost || s.caps.PromptInject, UserMessage: s.caps.UserMessage})
	now := time.Now().UTC()
	a := recovery.Attempt{Version: "recovery/1", ID: fp.Hash(s.Agent, s.ID, v.ID, key, now.Format(time.RFC3339Nano)), Agent: s.Agent, SessionID: s.ID, Revision: s.contractRevision, CauseKey: key, VerdictID: v.ID, Rule: v.Rule, Seq: v.Seq, Prescription: p, Stage: recovery.Proposed, CreatedAt: now, Route: decision.Route}
	if target != nil && target.Tool == event.ToolShell && target.ExecCertain && target.ExecFP != "" && (strings.HasPrefix(v.Rule, "s1.") || strings.HasPrefix(v.Rule, "s2.")) {
		a.ObservationBasis = "completed_command"
		a.TargetHash = fp.Hash(target.ExecFP)
	} else if p, ok := v.Facts["path"].(string); ok && p != "" {
		a.ObservationBasis = "same_path"
		a.TargetHash = fp.Hash(p)
	} else if v.Rule == "s4.read_only_streak" {
		a.ObservationBasis = "produce_after_read_streak"
	}
	if decision.Route == "observe" {
		a.DeliveryReason = "no_supported_advice_route"
	}
	s.recoveries = append(s.recoveries, a)
	s.saveRecovery(a)
	if decision.Route == "observe" {
		a.Observation = "unsupported_delivery"
		_ = a.Advance(recovery.Censored, now)
		s.recoveries[len(s.recoveries)-1] = a
		s.saveRecovery(a)
		return a, "", false
	}
	message := intervene.Recovery(a)
	return a, message, true
}

func (s *Session) saveRecovery(a recovery.Attempt) {
	if s.db != nil {
		s.recordStorageError(s.db.SaveRecovery(a))
	}
}

// markRecoveryOutput records only markers that survived capability filtering
// and dialect translation. Delivered means printed by the hook client, not
// read, agreed to, or acted on by the agent/user.
func (s *Session) markRecoveryOutput(out json.RawMessage, stage recovery.Stage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	for i := range s.recoveries {
		a := &s.recoveries[i]
		if !strings.Contains(string(out), recoveryMarker(a.ID)) {
			continue
		}
		if a.Stage == stage {
			continue
		}
		if err := a.Advance(stage, time.Now()); err != nil {
			continue
		}
		if stage == recovery.Delivered {
			a.DeliveredSeq = a.Seq
			if n := len(s.eng.St.Events); n > 0 && s.eng.St.Events[n-1].Seq > a.DeliveredSeq {
				a.DeliveredSeq = s.eng.St.Events[n-1].Seq
			}
		}
		s.saveRecovery(*a)
		if s.db != nil {
			if stage == recovery.Emitted {
				_, err := s.db.Exec(`UPDATE intervention SET delivery_state='emitted',emitted_at=? WHERE session_id=? AND verdict_id=?`, time.Now().UTC().Format(time.RFC3339Nano), s.ID, a.VerdictID)
				s.recordStorageError(err)
			}
			if stage == recovery.Delivered {
				_, err := s.db.Exec(`UPDATE intervention SET delivery_state='delivered',delivered_at=? WHERE session_id=? AND verdict_id=?`, time.Now().UTC().Format(time.RFC3339Nano), s.ID, a.VerdictID)
				s.recordStorageError(err)
			}
		}
	}
}

// relevantRecoveryEvent requires an actual completed action, not a message or
// an unrelated read. Hashes link the target without retaining command/path text.
func relevantRecoveryEvent(a recovery.Attempt, ev *event.Event) bool {
	if ev.Kind != event.KindTool {
		return false
	}
	switch a.ObservationBasis {
	case "completed_command":
		return ev.Tool == event.ToolShell && !ev.Background && ev.ExitCode != nil && ev.ExecCertain && ev.ExecFP != "" && fp.Hash(ev.ExecFP) == a.TargetHash
	case "same_path":
		if ev.Tool != event.ToolWrite && ev.Tool != event.ToolEdit {
			return false
		}
		for _, p := range ev.Paths {
			if fp.Hash(p) == a.TargetHash {
				return true
			}
		}
	case "produce_after_read_streak":
		return (ev.Tool == event.ToolWrite || ev.Tool == event.ToolEdit) && !ev.IsError && len(ev.Paths) > 0
	}
	return false
}

func (s *Session) trackOutcomes(ev *event.Event) {
	for i := range s.recoveries {
		a := &s.recoveries[i]
		if a.Stage == recovery.EffectObserved || a.Stage == recovery.Censored {
			continue
		}
		start := a.Seq
		if a.DeliveredAt != nil {
			start = a.DeliveredSeq
		}
		if ev.Seq <= start || ev.Seq <= a.ObservedThrough {
			continue
		}
		a.ObservedThrough = ev.Seq
		if a.DeliveredAt != nil && a.Revision == s.contractRevision && relevantRecoveryEvent(*a, ev) {
			a.RelevantOpportunities++
		}
		if ev.Seq < start+10 {
			s.saveRecovery(*a)
			continue
		}
		a.Observation = "delivery_unconfirmed"
		next := recovery.Censored
		if a.DeliveredAt != nil {
			a.Observation = "no_relevant_observation"
			if a.Revision != s.contractRevision {
				a.Observation = "target_revision_changed"
			}
			if a.Revision == s.contractRevision && a.RelevantOpportunities > 0 {
				// An asynchronous external audit cannot prove absence of a finding just
				// because its result has not arrived. Path-only opportunities stay unknown.
				a.Observation = "related_check_unconfirmed"
				if a.ObservationBasis != "same_path" {
					a.Observation = "no_recurrence_observed"
					next = recovery.EffectObserved
				}
				for _, v := range s.eng.Verdicts {
					if v.Rule != a.Rule || v.Seq <= start || v.Seq > ev.Seq || v.Level <= detect.L0 {
						continue
					}
					matched := false
					if a.ObservationBasis == "same_path" {
						p, _ := v.Facts["path"].(string)
						matched = p != "" && fp.Hash(p) == a.TargetHash
					} else if a.ObservationBasis == "produce_after_read_streak" {
						matched = true
					} else {
						for _, seen := range s.eng.St.Events {
							if seen.Seq == v.Seq && relevantRecoveryEvent(*a, seen) {
								matched = true
								break
							}
						}
					}
					if matched {
						a.Observation = "recurrence_observed"
						next = recovery.EffectObserved
						break
					}
				}
			}
		}
		_ = a.Advance(next, time.Now())
		if next == recovery.Censored {
			s.dropRecoveryMessage(a.ID)
		}
		s.saveRecovery(*a)
		if s.db != nil {
			_, err := s.db.Exec(`UPDATE intervention SET outcome=? WHERE session_id=? AND verdict_id=?`, a.Observation, s.ID, a.VerdictID)
			s.recordStorageError(err)
		}
	}
}

func (s *Session) censorRecoveries(reason string) {
	for i := range s.recoveries {
		a := &s.recoveries[i]
		if a.Stage == recovery.EffectObserved || a.Stage == recovery.Censored {
			continue
		}
		a.Observation = reason
		_ = a.Advance(recovery.Censored, time.Now())
		s.dropRecoveryMessage(a.ID)
		s.saveRecovery(*a)
	}
}

func (s *Session) dropRecoveryMessage(id string) {
	keep := func(messages []string) []string {
		out := messages[:0]
		for _, m := range messages {
			if !strings.Contains(m, recoveryMarker(id)) {
				out = append(out, m)
			}
		}
		return out
	}
	s.pending = keep(s.pending)
	s.userMsg = keep(s.userMsg)
}

// An explicit keep/steer command acknowledges this interface choice; it is
// not proof that a model executed the prescription or that a human read it.
func (s *Session) acknowledgeRecovery(verdictID string) {
	for i := range s.recoveries {
		a := &s.recoveries[i]
		if a.VerdictID == verdictID && a.Stage == recovery.Delivered {
			if err := a.Advance(recovery.Acknowledged, time.Now()); err == nil {
				s.saveRecovery(*a)
			}
		}
	}
}

func (d *Daemon) recoverySession(req Request, out json.RawMessage) *Session {
	if !strings.Contains(string(out), "[sc-recovery:") {
		return nil
	}
	agent := req.Agent
	if agent == "" {
		agent = "claude"
	}
	var id string
	if agent == "agy" {
		// translating an agy payload again would move held results
		var a agyPayload
		if json.Unmarshal(req.Payload, &a) != nil {
			return nil
		}
		id = a.ConversationID
	} else {
		_, inputs, err := translateIn(agent, req.Event, req.Payload)
		if err != nil || len(inputs) == 0 {
			return nil
		}
		id = inputs[0].SessionID
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sessions[id]
}
