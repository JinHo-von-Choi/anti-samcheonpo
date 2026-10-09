package detect

import (
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// marathonState holds what the long-session rules need: the change since the
// last whole-suite run, passed probes of long runs, findings reports being
// worked through one by one, repeated rejections by tools outside the known
// checks, remote CI and release history, browser-test timing failures and the
// session's active time.
type marathonState struct {
	scope       scopeState
	probes      map[string]probeMark
	triage      map[string]*triageMark
	lastRead    string // findings report read or queried most recently
	rejects     map[string]*rejectMark
	ci          *ciMark
	releases    []time.Time
	lastRelease int64 // seq of the latest release that went out
	released    bool
	ui          map[string]*uiMark
	// scripts maps runner scripts written from a here-document to the
	// tests they run; checkCost is the last run time of a test run by key.
	scripts   map[string]scriptMark
	checkCost map[string]time.Duration

	active     time.Duration
	lastTS     time.Time
	tierFired  map[int]bool
	extraHours float64
	extraTok   int64
}

func newMarathonState() *marathonState {
	return &marathonState{probes: map[string]probeMark{}, triage: map[string]*triageMark{}, rejects: map[string]*rejectMark{},
		ui: map[string]*uiMark{}, tierFired: map[int]bool{}, scripts: map[string]scriptMark{}, checkCost: map[string]time.Duration{}}
}

// activeGap is the longest pause between two events counted as work. A longer
// pause is the user away, unless a tool call was running through it.
const activeGap = 30 * time.Minute

// trackActive adds the time since the previous event to the session's active
// time.
func (e *Engine) trackActive(ev *event.Event) {
	m := e.St.mar
	if ev.TS.IsZero() {
		return
	}
	if !m.lastTS.IsZero() && ev.TS.After(m.lastTS) {
		gap := ev.TS.Sub(m.lastTS)
		switch {
		case gap <= activeGap:
			m.active += gap
		case ev.DurationMS > 0:
			m.active += min(gap, time.Duration(ev.DurationMS)*time.Millisecond)
		}
	}
	if ev.TS.After(m.lastTS) {
		m.lastTS = ev.TS
	}
}

// marathon updates the long-session rules after a tool event. In audit mode
// the rules that live mode applies before a call runs are raised here, from
// the state before this event, so a receipt counts them as well.
func (e *Engine) marathon(ev *event.Event, sigs *[]Signal) {
	// a call the live harness refused never ran and was judged then
	if e.Mode != "live" && (ev.ExitCode == nil || *ev.ExitCode != -1) {
		if s := e.preGuard(ev, false); s != nil {
			e.add(sigs, *s)
		}
		if s := e.rejectedRerun(ev, false); s != nil {
			e.add(sigs, *s)
		}
	}
	e.trackScope(ev)
	e.trackScripts(ev)
	e.trackProbe(ev)
	e.trackTriage(ev, sigs)
	e.trackRejection(ev, sigs)
	e.trackRelease(ev)
	e.trackUITiming(ev, sigs)
}

// PreGuard is the live check before a call that does not depend on the
// workspace fingerprint: the session ceiling, releases, long runs and
// whole-suite runs after a local change.
func (e *Engine) PreGuard(ev *event.Event) *Signal { return e.preGuard(ev, true) }

func (e *Engine) preGuard(ev *event.Event, live bool) *Signal {
	e.refine(ev)
	if live {
		// the ceiling is judged on the live clock and usage; an audit replays
		// it from the events through cost()
		if s := e.ceilingCheck(ev); s != nil {
			return s
		}
	}
	if s := e.releaseCheck(ev, live); s != nil {
		return s
	}
	if s := e.longRunCheck(ev, live); s != nil {
		return s
	}
	return e.fullSuiteCheck(ev, live)
}
