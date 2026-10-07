package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
)

// markCompaction notes that the conversation was compacted, so the next
// response the agent can read carries the contract anchor once. A normal turn
// injects nothing: the anchor exists only to survive the loss of context.
// Called with s.mu held.
func (s *Session) markCompaction() { s.anchorDue = true }

// anchorInfo is the contract state worth repeating after compaction: the
// goal, and for an accepted contract its boundaries and remaining conditions.
// Called with s.mu held.
func (s *Session) anchorInfo() hookclient.ContractAnchorInfo {
	info := hookclient.ContractAnchorInfo{}
	switch {
	case s.c != nil && s.c.Goal != "":
		info.Goal = s.c.Goal
	case s.intentRevision != nil && s.intentRevision.Goal != "":
		info.Goal = s.intentRevision.Goal
	default:
		info.Goal = s.firstPrompt
	}
	if s.c == nil || s.acc.State != contract.StateAccepted {
		return info
	}
	info.AllowPatterns = s.c.Scope.Allow
	info.ProtectPatterns = s.c.Scope.Protect
	if s.c.Budget.KRW > 0 {
		info.BudgetRemainingKRW = max(0, s.c.Budget.KRW-cost.Won(s.eng.St.TotalMicro))
	}
	info.PassedChecks, info.TotalChecks = s.criteria()
	return info
}

// takeAnchor returns the anchor line if one is due and the hook can carry it,
// and clears the debt. A hook that cannot inject leaves the debt standing for
// the next one that can. Called with s.mu held.
func (s *Session) takeAnchor(canInject bool) string {
	if !s.anchorDue || !canInject {
		return ""
	}
	info := s.anchorInfo()
	if info.Goal == "" {
		// nothing to anchor to; the debt is void, not deferred
		s.anchorDue = false
		return ""
	}
	s.anchorDue = false
	return hookclient.FormatAnchorHeader(info)
}
