package live

import (
	"encoding/json"
	"fmt"
	"strings"

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
func (s *Session) stopHold(sigs []detect.Signal, unmet []string, shadow bool) *detect.Signal {
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
		if s.verified() {
			// a check passed after the last change: the gate is open
			return nil
		}
		held := v // a copy: the caller removes it from sigs in place
		return &held
	}
	return nil
}

// verified reports whether a check passed after the last change the session
// made to the work. The gate opens only on this: a change that has never
// passed a check, and a check that failed and was not repeated, are both
// unverified work.
//
// A session that changed no work of its own is covered by any check that
// passed, so the gate opens on it as well.
func (s *Session) verified() bool {
	return s.eng.VerifiedSince(s.eng.St.LastProductionWrite())
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
		s.recordBlock(v)
	}
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
