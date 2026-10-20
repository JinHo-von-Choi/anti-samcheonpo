// Package hostlimit reports host resource limits that bound samcheonpo's
// observation work.
//
// The daemon watches each live session's workspace with fsnotify. On Linux
// every watcher holds one inotify instance, and the kernel limits how many
// instances one user may hold. When that limit is reached, fsnotify fails to
// open a watcher and the workspace fingerprint stops updating; the monitor
// then reports "available" while observing nothing. Counting the budget here
// lets the failure surface before it is silent.
package hostlimit

// Watch describes the host's file-watch instance budget.
type Watch struct {
	// Used is the number of watch instances the current user holds.
	Used int
	// Limit is the per-user instance limit; zero when the host has none.
	Limit int
	// Known is false when the host does not expose a limit to read.
	Known bool
}

// Ratio returns the share of the limit in use. It is zero when the host
// exposes no limit, because an unknown budget is not a full one.
func (w Watch) Ratio() float64 {
	if !w.Known || w.Limit <= 0 {
		return 0
	}
	return float64(w.Used) / float64(w.Limit)
}

// Remaining returns the instances still available. It is negative when the
// user is already over the limit.
func (w Watch) Remaining() int {
	if !w.Known {
		return 0
	}
	return w.Limit - w.Used
}

// Saturated reports whether the budget is exhausted or nearly so. A watcher
// that cannot be opened leaves the monitor blind, so the margin matters more
// than the exact count.
func (w Watch) Saturated() bool {
	if !w.Known || w.Limit <= 0 {
		return false
	}
	return w.Used >= w.Limit
}
