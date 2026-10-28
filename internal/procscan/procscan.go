// Package procscan lists the running processes a session left behind in its
// workspace. Ownership is not provable once a process is re-parented to
// init, so a process counts when it runs inside the workspace, belongs to
// the same user, is not a system service, and started while one of the
// session's shell calls was running.
package procscan

import "time"

// Proc is one running process.
type Proc struct {
	PID     int
	Started time.Time
	Exe     string // executable path when readable
	Cmd     string // command line, shortened
	Ports   []int  // TCP ports it listens on
}

// Window is a span of time a shell call of the session ran in.
type Window struct{ From, To time.Time }

func inWindows(t time.Time, ws []Window) bool {
	for _, w := range ws {
		if !t.Before(w.From) && !t.After(w.To) {
			return true
		}
	}
	return false
}
