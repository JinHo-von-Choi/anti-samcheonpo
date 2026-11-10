package live

import (
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// RuleSlowStop records a turn end that took long enough to hold the agent.
// It is level 0: a measurement for finding the cause, never advice.
const RuleSlowStop = "s8.slow_stop"

const slowStopThreshold = time.Second

// stopPhases is how long each part of a turn end took.
type stopPhases struct{ flush, checks, scan, total time.Duration }

// noteSlowStop stores the phases of a slow turn end. The caller holds s.mu.
func (s *Session) noteSlowStop(ev *event.Event, p stopPhases) {
	if p.total < slowStopThreshold {
		return
	}
	s.deliver(s.eng.Emit(ev, []detect.Signal{{Detector: "S8", Rule: RuleSlowStop, Confidence: 1, Level: detect.L0, Evidence: []int64{ev.Seq},
		Facts: map[string]any{"total_ms": p.total.Milliseconds(), "flush_ms": p.flush.Milliseconds(), "checks_ms": p.checks.Milliseconds(), "process_scan_ms": p.scan.Milliseconds(), "other_ms": (p.total - p.flush - p.checks - p.scan).Milliseconds()}}}))
}
