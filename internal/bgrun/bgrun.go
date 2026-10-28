// Package bgrun completes shell calls an agent started in the background.
// The start keeps its own event and its final result arrives later as a new
// event in arrival order, carrying the start's command and the workspace it
// ran on, so a change made while it ran is not counted as checked by it.
package bgrun

import (
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testout"
)

// Outcome is the final state of a background run.
type Outcome struct {
	State   string // exited | failed | timeout | canceled | unknown
	Failure string
	Exit    *int
	Text    string
	TaskID  string
	Source  string // what stated the outcome (Execution.ExitSource)
}

// Completion builds the completion event of a background start. ended is
// when the agent stated the run ended (zero when it did not); a stated end
// before the launch or after now is not used.
func Completion(start *event.Event, o Outcome, ended, now time.Time) *event.Event {
	launched := start.TS.Add(-time.Duration(start.DurationMS) * time.Millisecond)
	end := now
	if !ended.IsZero() && !ended.After(now) && !ended.Before(launched) {
		end = ended
	}
	ev := &event.Event{
		Kind: start.Kind, Tool: start.Tool, RawTool: start.RawTool, CallID: start.CallID + "#result",
		Cmd: start.Cmd, CmdNorm: start.CmdNorm, CmdFP: start.CmdFP, ExecFP: start.ExecFP, ExecCertain: start.ExecCertain,
		TimeoutMS: start.TimeoutMS, Dir: start.Dir, Category: start.Category, ShellClass: start.ShellClass,
		Mutating: start.Mutating, Unknown: start.Unknown, WSBefore: start.WSBefore, WSAfter: start.WSBefore,
		OriginSeq: start.Seq, TS: end, Basis: start.Basis, ResultObserved: true, Summary: start.Summary,
		Parent: start.Parent, SourceRef: start.SourceRef,
	}
	if !start.TS.IsZero() && end.After(launched) {
		ev.DurationMS = end.Sub(launched).Milliseconds()
	}
	if start.Reliability != nil {
		rel := *start.Reliability
		rel.Phase = "result"
		rel.SourceMissing = o.State == "unknown"
		ev.Reliability = &rel
	}
	SetShellResult(ev, o.Exit, o.Text)
	switch o.State {
	case "canceled":
		// a stopped task ran to no result; it is neither a pass nor a fail
		ev.ExitCode = nil
		ev.IsError = false
	case "timeout":
		ev.IsError = true
	}
	ev.Execution = &event.Execution{State: o.State, ExitSource: o.Source, CallID: start.CallID, ProcessID: o.TaskID, ObservedAt: now, FailureType: o.Failure}
	ev.ResultFP = fp.ResultFP(ev.ExitCode, ev.ErrFPs, ev.FailedTests)
	return ev
}

// SetShellResult stores a shell exit code and the output excerpt the rules
// read. A nil exit code stays unknown.
func SetShellResult(ev *event.Event, exit *int, out string) {
	if len(out) > 4000 {
		out = out[len(out)-4000:]
	}
	ev.Text = out
	ev.ErrFPs, ev.FailedTests = nil, nil
	if out != "" {
		ev.ObservationFP = fp.Hash(out)
		ev.ErrFPs = fp.ErrorFPs(out)
		ev.FailedTests = testout.FailedTests(out)
	}
	ev.ExitCode = exit
	if exit != nil {
		ev.IsError = *exit != 0
	}
}
