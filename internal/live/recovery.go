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
	behaviorTarget, _ := v.Facts["target_hash"].(string)
	key := fp.Hash(string(cause), v.Rule, strings.Join(signatures, "|"), behaviorTarget)
	p, ok := recovery.Select(cause, key, s.contractRevision, s.recoveries)
	if !ok {
		return recovery.Attempt{}, "", false
	}
	switch v.Rule {
	case "s1.linked_loop":
		p.Action = "연결된 세션의 동일 대상·결과 근거를 합치고, 새 입력·환경·결과를 확인할 다음 행동을 선택한다."
		p.StopCondition = "새 근거가 없으면 같은 재시도를 멈추고 남은 질문이나 실패 원인을 정리한다."
	case "s4.research_repeat":
		p.Action = "미해결 질문 하나와 이미 얻은 근거를 대조하고, 새 근거가 필요한 검색만 선택한다."
		p.StopCondition = "같은 자료뿐이면 검색을 멈추고 현재 근거와 남은 질문을 정리한다."
	case "s1.delegation_loop":
		p.Action = "같은 리뷰를 재위임하기 전에 새 관점·추가 근거·담당자를 정리한다."
		p.StopCondition = "새 근거가 없으면 현재 결과를 합치고 남은 완료 조건을 확인한다."
	case "s5.abandoned_verification":
		p.Action = "실패한 검사를 고치거나, 마지막 변경에 맞는 가장 작은 검사를 직접 실행해 결과를 확인한다."
		p.StopCondition = "외부 원인(권한·네트워크·외부 서비스)이나 사용자의 중단 지시가 있을 때만 근거와 함께 보고하고 멈춘다."
	case "s5.partial_done":
		p.Action = "요청 항목별 결과와 누락을 대조하고, 승인 범위 안에서 남은 작업을 계속한다."
		p.StopCondition = "부분 산출물과 전체 완료를 구분하고 항목별 근거를 남긴다."
	case "s3.repeated_permission":
		p.Action = "이미 명시된 승인 범위를 확인하고 그 안의 다음 작업을 진행한다."
		p.StopCondition = "범위·외부 영향·복구 가능성이 바뀌는 선택만 사용자에게 확인한다."
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
	if completionRule(v.Rule) {
		// judged at turn end, so its effect is settled at the next turn end
		a.ObservationBasis = "repair_after_completion"
	} else if hash, ok := v.Facts["target_hash"].(string); ok && hash != "" {
		a.ObservationBasis = "behavior_target"
		a.TargetHash = hash
	} else if target != nil && target.Tool == event.ToolShell && target.ExecCertain && target.ExecFP != "" && (strings.HasPrefix(v.Rule, "s1.") || strings.HasPrefix(v.Rule, "s2.")) {
		a.ObservationBasis = "completed_command"
		a.TargetHash = fp.Hash(target.ExecFP)
	} else if p, ok := v.Facts["path"].(string); ok && p != "" {
		a.ObservationBasis = "same_path"
		a.TargetHash = fp.Hash(p)
	} else if v.Rule == "s4.read_only_streak" {
		a.ObservationBasis = "produce_after_read_streak"
	}
	if decision.Route == "observe" {
		a.DeliveryReason = s.observeDeliveryCause()
		s.recordDecision(v, "unsupported_route")
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

// observeDeliveryCause names why a recovery cannot be delivered while the
// route is observe: payload shape is read live (s.shapeNote, cleared by
// noteShapeGood), everything else is the agent's observe-only profile.
// proposeRecovery holds s.mu, which guards both fields.
func (s *Session) observeDeliveryCause() string {
	if s.shapeNote != "" {
		return "shape_bad"
	}
	if s.observeCause != "" {
		return s.observeCause
	}
	return "caps_observe"
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
	case "behavior_target":
		return !ev.Background && ev.BehaviorTarget != "" && ev.BehaviorTarget == a.TargetHash && (ev.ExitCode != nil || ev.BehaviorResult != "")
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
	case "repair_after_completion":
		// the repair a completion verdict asks for: a code change or a check run
		return (ev.Tool == event.ToolWrite || ev.Tool == event.ToolEdit) && !ev.IsError && len(ev.Paths) > 0 ||
			ev.Tool == event.ToolShell && !ev.Background && ev.ExitCode != nil
	}
	return false
}

// completionRule reports rules judged when the agent ends a turn. Their
// effect is a repair after delivery, and a recurrence is the same verdict
// at a later turn end.
func completionRule(rule string) bool {
	switch rule {
	case "s5.false_done", "s5.stop_unmet", "s5.abandoned_verification", "s5.partial_done":
		return true
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
					} else if a.ObservationBasis == "produce_after_read_streak" || a.ObservationBasis == "repair_after_completion" {
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

// settleCompletion observes the effect of delivered completion
// prescriptions at a turn end. A repair (a code change or a check run) and
// no new verdict of the rule is no recurrence; the same verdict again is a
// recurrence. A turn end with no repair yet leaves it open. Called with
// s.mu held.
func (s *Session) settleCompletion(sigs []detect.Signal) {
	for i := range s.recoveries {
		a := &s.recoveries[i]
		if a.ObservationBasis != "repair_after_completion" || a.DeliveredAt == nil || a.Stage == recovery.EffectObserved || a.Stage == recovery.Censored {
			continue
		}
		recurred := false
		for _, v := range sigs {
			if v.Rule == a.Rule && v.Level > detect.L0 {
				recurred = true
			}
		}
		if !recurred && a.RelevantOpportunities == 0 {
			continue
		}
		a.Observation = "no_recurrence_observed"
		if recurred {
			a.Observation = "recurrence_observed"
		}
		_ = a.Advance(recovery.EffectObserved, time.Now())
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
