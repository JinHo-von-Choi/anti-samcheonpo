package live

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intervene"
)

// stopHold picks the abandoned-verification verdict that holds this turn
// end open, or nil.
//
// A hold happens once per evidence: the same failing check or the same
// unverified change does not hold twice, and a turn the host already
// continued after a stop hook (active) is not held again, so the agent is
// never kept in a loop. Unmet accepted checks have their own stop path.
// Called with s.mu held.
func (s *Session) stopHold(sigs []detect.Signal, unmet []string, active, shadow bool) *detect.Signal {
	if shadow || len(unmet) > 0 {
		return nil
	}
	for i := range sigs {
		v := sigs[i]
		if v.Rule != detect.RuleAbandonedVerification || !v.Primary || v.Arm == "none" {
			continue
		}
		if route, _ := s.routeFor(v); route == "observe" || s.released[s.adviceKey(v)] {
			return nil
		}
		if !s.caps.BlockStop {
			s.recordDecision(v, "block_unsupported_by_host")
			return nil
		}
		key := stopHoldKey(v)
		if active || s.heldStops[key] || s.stopBlocks >= s.Cfg.Levels.MaxStopBlocks {
			return nil
		}
		s.heldStops[key] = true
		held := v // a copy: the caller removes it from sigs in place
		return &held
	}
	return nil
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
	s.stopBlocks++
	s.blocks++
	s.lastPrimary = &v
	s.persistVerdict(v)
	s.recordBlock(v)
	arm := "prescription"
	if v.Arm == "fact" {
		arm = "fact"
	}
	reason := intervene.Agent(v, s.msgContext(), arm)
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
