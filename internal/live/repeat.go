package live

import (
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/policy"
)

// Cross-session repetition: the same rule delivered on the same path of a
// project again and again, with nobody confirming it, is noise to the agent.
// A scheduled job that repeats one task opens a new session each time, so
// the in-session cooldown never sees the repetition.
const (
	repeatDeliveries = 3
	repeatWindow     = 30 * 24 * time.Hour
)

// repeatedAcrossSessions reports whether a verdict is recorded only because
// earlier sessions already delivered it on the same path. Guardrails the
// user set are never muted. Called with s.mu held.
func (s *Session) repeatedAcrossSessions(v detect.Signal) bool {
	if s.db == nil || policy.ExplicitGuardrail(v.Rule) {
		return false
	}
	path, _ := v.Facts["path"].(string)
	if path == "" {
		return false
	}
	n, err := s.db.DeliveredOnPath(s.Root, v.Rule, path, time.Now().Add(-repeatWindow))
	if err != nil {
		s.recordStorageError(err)
		return false
	}
	return n >= repeatDeliveries
}
