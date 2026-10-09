// Package clock measures short intervals at a resolution fine enough for hook
// latency budgets. Go's own monotonic clock follows the system timer tick on
// Windows, so an interval shorter than a tick reads as zero there.
package clock

import "time"

// Stamp marks a moment taken with Now.
type Stamp struct {
	wall time.Time
	tick int64
}

// Now marks the current moment.
func Now() Stamp { return Stamp{wall: time.Now(), tick: counter()} }

// Since returns the time elapsed since the stamp.
func (s Stamp) Since() time.Duration { return since(s) }
