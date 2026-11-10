package live

import (
	"encoding/json"
	"fmt"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intervene"
)

// blockStopForQuality keeps the turn going with the reason the work cannot
// end yet. Each reason is used once per request, so a turn cannot be held
// forever by a judgment the agent answers honestly.
func (s *Session) blockStopForQuality(v detect.Signal, seq int64) json.RawMessage {
	s.mu.Lock()
	ctx := s.msgContext()
	v.Seq, v.ID, v.Primary = seq, fmt.Sprintf("%s-quality-%d-%s", s.ID, seq, v.Rule), true
	s.mu.Unlock()
	reason := intervene.Agent(v, ctx, "prescription")
	s.storeAfterAnswer(func() {
		s.persistPreVerdict(v)
		s.recordBlock(v)
		s.broadcastHUD()
	})
	// what the work wrote is read from the answer itself, so a lost
	// observation does not make this judgment unsafe
	s.budgetDeny.Store(true)
	return hookOut(map[string]any{"decision": "block", "reason": reason})
}
