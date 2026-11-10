package live

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intervene"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/receipt"
)

// stopGateRule reports whether a rule holds the turn end until the work is
// verified.
//
// A false completion and an abandoned verification are one gate seen from
// two sides: the turn ends while a check could still run. The end is
// refused, not annotated.
func stopGateRule(rule string) bool {
	return rule == detect.RuleAbandonedVerification || rule == detect.RuleFalseDone
}

// stopHold picks the verdict that holds this turn end open, or nil.
//
// The hold is a gate, not a warning. A turn that ends while its own change
// is unverified, or while the check that failed is still failing, is
// refused, and it stays refused until a check has passed on the work as it
// stands. Neither the host asking again (StopHookActive) nor the same
// evidence coming back releases it: the state the hold names is still
// true, and a turn end that is released while a check is still failing is
// the false completion this rule exists to stop.
//
// The verdict that holds the end may be a suppressed one. The cooldown
// keeps a finding from being advised twice in a row, but a refusal of the
// turn end is not advice: the work is still unverified, so the end is
// still refused.
//
// Unmet accepted checks have their own stop path. Called with s.mu held.
func (s *Session) stopHold(sigs []detect.Signal, unmet []string, shadow bool, fresh []CheckResult) *detect.Signal {
	if shadow || len(unmet) > 0 {
		return nil
	}
	for i := range sigs {
		v := sigs[i]
		if !stopGateRule(v.Rule) || v.Level == detect.L0 {
			continue
		}
		if route, _ := s.routeFor(v); route == "observe" || s.released[s.adviceKey(v)] {
			return nil
		}
		if !s.caps.BlockStop {
			s.recordDecision(v, "block_unsupported_by_host")
			return nil
		}
		if s.verified() || s.freshTypedAcceptance(fresh) {
			// a check passed after the last change: the gate is open
			return nil
		}
		if release := s.stopRelease(v); release != "" {
			s.recordDecision(v, release)
			return nil
		}
		s.stopHolds++
		held := v // a copy: the caller removes it from sigs in place
		return &held
	}
	return nil
}

// stopRelease names why a gate that would hold this turn end lets it
// through, or "" to hold it.
//
// The gate cannot tell a check it does not understand from no check at
// all. Without a way out, a check the agent ran but samcheonpo cannot relate
// to the changed file holds every turn end of the session, and the agent
// answers each hold by restating that it is done. So the gate is bounded:
//
//   - the user let the next turn end through (keep now);
//   - the hold names no verification at all, yet a check passed after the
//     last change that samcheonpo cannot judge (a Gradle build, a piped
//     run): the agent verified something the coverage model does not know;
//   - the same work was held MaxStopHolds times with no change in between.
//
// Each release is recorded as a decision and told to the user, so an
// unverified end stays visible instead of being held forever.
// Called with s.mu held.
func (s *Session) stopRelease(v detect.Signal) string {
	write := s.eng.St.LastProductionWrite()
	if write != s.stopHoldWrite {
		s.stopHolds, s.stopHoldWrite = 0, write
	}
	target, _ := v.Facts["path"].(string)
	if target == "" {
		target = s.eng.St.LastProductionPath()
	}
	where := ""
	if target != "" {
		where = " (`" + target + "`)"
	}
	switch {
	case s.stopPassOnce:
		s.stopPassOnce = false
		s.stopNotice = s.pick("[삼천포] 요청하신 대로 이번 종료는 보류하지 않았습니다. 마지막 변경"+where+"은 아직 검증되지 않았습니다.", "[samcheonpo] As you asked, this stop was not held. The last change"+where+" has not been verified.")
		return "stop_hold_passed_once"
	case v.Rule == detect.RuleAbandonedVerification && s.eng.PassedUnjudgedSince(write):
		s.stopNotice = s.pick("[삼천포] 마지막 변경 뒤에 성공한 검사가 있지만 변경 파일"+where+"과 연결할 수 없었습니다. 미검증 상태로 종료를 허용했습니다.", "[samcheonpo] A check passed after the last change but could not be tied to the changed files"+where+". The stop was allowed as unverified.")
		return "stop_hold_unrelated_pass"
	case s.Cfg.Levels.MaxStopHolds > 0 && s.stopHolds >= s.Cfg.Levels.MaxStopHolds:
		s.stopNotice = fmt.Sprintf(s.pick("[삼천포] 같은 변경%s으로 종료를 %d번 보류했지만 검사로 확인하지 못했습니다. 미검증 상태로 종료를 허용했습니다.", "[samcheonpo] The stop was held %[2]d times for the same change%[1]s but no check confirmed it. The stop was allowed as unverified."), where, s.stopHolds)
		return "stop_hold_exhausted"
	}
	return ""
}

// Only the checks executed by this Stop may release a typed, non-reusable
// criterion. A mutation/intent change clears s.checks; a later checkpoint has
// different observation identities. No manual criterion is auto-confirmed.
func (s *Session) freshTypedAcceptance(fresh []CheckResult) bool {
	if s.c == nil || s.acc.State != contract.StateAccepted || len(s.c.Done) == 0 || len(fresh) != len(s.c.Done) || s.storageErr != nil || s.queueRejected.Load() > 0 {
		return false
	}
	for _, check := range s.c.Done {
		if check.Manual != "" {
			return false
		}
		found := false
		for _, r := range fresh {
			current, ok := s.checks[r.ID]
			if r.ID == check.ID && ok && r.Pass && !r.Reused && !r.SideEffect && !r.TimedOut && !r.Truncated && r.Skipped == "" && len(r.Failed) == 0 && r.Observation != nil && r.Observation.State == "passed" && current.Observation == r.Observation {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// verified reports whether a check passed after the last change the session
// made to the work. The gate opens only on this: a change that has never
// passed a check, and a check that failed and was not repeated, are both
// unverified work.
//
// A session that changed no work of its own is covered by any check that
// passed, so the gate opens on it as well.
func (s *Session) verified() bool {
	// An accepted contract can define custom checks the command classifier
	// cannot understand. Require fresh evidence for every accepted criterion.
	return s.eng.VerifiedSince(s.eng.St.LastProductionWrite()) || s.reliabilityCriteria(time.Now())
}

// stopHoldKey names the evidence a hold answers.
func stopHoldKey(v detect.Signal) string {
	at := int64(0)
	if len(v.Evidence) > 0 {
		at = v.Evidence[0]
	}
	return fmt.Sprintf("%v|%v|%v|%d", v.Facts["kind"], v.Facts["cmd"], v.Facts["path"], at)
}

// holdStop keeps the turn going with the concrete next step.
func (s *Session) holdStop(v detect.Signal, in HookInput) json.RawMessage {
	nudge, _ := s.takePendingFor("StopBlock", in.Ack)
	s.mu.Lock()
	key := stopHoldKey(v)
	first := !s.heldStops[key]
	s.heldStops[key] = true
	s.blocks++
	s.lastPrimary = &v
	if first {
		// One intervention per evidence. The gate restates the same
		// prescription until a check passes, and a restated prescription
		// is not a second prescription: the outcome of one delivery is
		// what the ledger measures.
		s.persistVerdict(v)
		s.recordActionPhase(v, "agent", true, "stop")
	}
	s.noteIntervention(in.Ack, v.ID)
	arm := v.Arm
	if arm == "" {
		arm = "prescription"
	}
	reason := intervene.Agent(v, s.msgContext(), arm)
	if reason == "" {
		reason = receipt.Describe(v)
	}
	if a, prescription, ok := s.proposeRecovery(v); ok {
		reason = armMessage(v, reason, a, prescription)
	}
	s.mu.Unlock()
	if nudge != "" {
		reason = nudge + "\n\n" + reason
	}
	s.mu.Lock()
	s.holdReason = strings.TrimSpace(reason)
	s.mu.Unlock()
	s.storeSnapshot()
	return hookOut(map[string]any{"decision": "block", "reason": reason})
}
