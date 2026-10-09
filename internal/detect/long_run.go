package detect

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// probeMark is the longest passed run of one long-run target and when it ran.
type probeMark struct {
	seconds int
	seq     int64
}

// longRun reads a shell call's expected run time: the time the command
// states, else for a load or benchmark run the timeout the agent asked for.
// expected is -1 for such a run started in the background with no stated
// time, 0 for a call that is not a long run.
func longRun(ev *event.Event) (info classify.LongRunInfo, expected int) {
	if ev.Tool != event.ToolShell {
		return info, 0
	}
	raw := ev.Cmd
	if raw == "" {
		raw = ev.CmdNorm
	}
	info = classify.LongRun(raw)
	switch {
	case info.Target == "":
		return info, 0
	case info.Explicit():
		return info, info.Seconds
	case ev.TimeoutMS > 0:
		return info, int(ev.TimeoutMS / 1000)
	case ev.Background:
		return info, -1
	}
	return info, 0
}

// trackProbe records a passed long-run target as a probe for longer runs.
func (e *Engine) trackProbe(ev *event.Event) {
	if ev.Tool != event.ToolShell || ev.ExitCode == nil || *ev.ExitCode != 0 || ev.Background {
		return
	}
	info, expected := longRun(ev)
	if info.Target == "" {
		return
	}
	secs := expected
	if ev.DurationMS > 0 {
		secs = int(ev.DurationMS / 1000)
	}
	if secs <= 0 {
		return
	}
	m := e.St.mar
	if p, ok := m.probes[info.Target]; !ok || secs >= p.seconds || p.seq <= e.St.lastCodeWriteSeq {
		m.probes[info.Target] = probeMark{seconds: secs, seq: ev.Seq}
	}
}

// longRunCheck raises s1.unprobed_long_run for a run longer than the free
// limit and longer than StepRatio times the longest passed run of the same
// target since the last code change. A run whose time is only inferred (a
// load-test name with a long timeout or in the background) is an estimate.
func (e *Engine) longRunCheck(ev *event.Event, live bool) *Signal {
	info, expected := longRun(ev)
	if expected == 0 {
		return nil
	}
	d := e.Cfg.Detectors.S1
	if d.LongRunFreeSec <= 0 {
		return nil
	}
	if expected > 0 && expected <= d.LongRunFreeSec {
		return nil
	}
	passed := 0
	if p, ok := e.St.mar.probes[info.Target]; ok && p.seq > e.St.lastCodeWriteSeq {
		passed = p.seconds
	}
	allowed := max(d.LongRunFreeSec, passed*d.LongRunStepRatio)
	if (expected > 0 && expected <= allowed) || (expected < 0 && passed > 0) {
		return nil
	}
	kind, conf := "explicit", 0.85
	if !info.Explicit() {
		kind, conf = "estimated", 0.6
	}
	facts := map[string]any{"cmd": ev.CmdNorm, "seconds": max(expected, 0), "allowed": allowed, "probe": passed, "kind": kind, "target": info.Target}
	if live {
		facts["blocked"] = true
	}
	return &Signal{Detector: "S1", Rule: "s1.unprobed_long_run", Confidence: conf, Level: L1, Evidence: []int64{ev.Seq}, Facts: facts, Estimate: kind == "estimated"}
}
