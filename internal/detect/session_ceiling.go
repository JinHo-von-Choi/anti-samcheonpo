package detect

import (
	"fmt"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// Session length tiers.
const (
	tierNotice  = 1 // shown on the status line
	tierWarn    = 2 // the user is asked whether to go on
	tierCeiling = 3 // the session stops until the user extends it
)

// SessionLength reports the session's active time, its fresh tokens (input,
// output, cache writes) and the highest tier reached. Caller serializes with
// engine mutation.
func (e *Engine) SessionLength() (active time.Duration, tokens int64, tier int) {
	active, tokens = e.St.mar.active, e.St.FreshTokens()
	d := e.Cfg.Detectors.S8
	h, t := e.ceilingLimits()
	switch {
	case over(active, tokens, h, t) != "":
		tier = tierCeiling
	case over(active, tokens, d.WarnHours, d.WarnTokens) != "":
		tier = tierWarn
	case over(active, tokens, d.NoticeHours, d.NoticeTokens) != "":
		tier = tierNotice
	}
	return active, tokens, tier
}

// ceilingLimits returns the hard limits in force: the configured ceiling, an
// accepted contract's time budget when it is lower, plus what the user
// extended. 0 is no limit.
func (e *Engine) ceilingLimits() (hours float64, tokens int64) {
	d := e.Cfg.Detectors.S8
	hours, tokens = d.CeilingHours, d.CeilingTokens
	if e.Contract != nil && e.Accepted && e.Contract.Budget.Minutes > 0 {
		if h := float64(e.Contract.Budget.Minutes) / 60; hours <= 0 || h < hours {
			hours = h
		}
	}
	m := e.St.mar
	if hours > 0 {
		hours += m.extraHours
	}
	if tokens > 0 {
		tokens += m.extraTok
	}
	return hours, tokens
}

// over names the limit a session is at or past: "time", "tokens" or "".
func over(active time.Duration, tokens int64, hours float64, limit int64) string {
	switch {
	case hours > 0 && active.Hours() >= hours:
		return "time"
	case limit > 0 && tokens >= limit:
		return "tokens"
	}
	return ""
}

// sessionLength raises the session-length tiers once each as they are
// reached: s8.session_long (notice L0, warn L2) and s8.session_ceiling (L3).
func (e *Engine) sessionLength(ev *event.Event, sigs *[]Signal) {
	m := e.St.mar
	active, tokens, tier := e.SessionLength()
	if tier == 0 || m.tierFired[tier] {
		return
	}
	for t := tier; t >= tierNotice; t-- {
		m.tierFired[t] = true
	}
	d := e.Cfg.Detectors.S8
	facts := lengthFacts(active, tokens)
	switch tier {
	case tierCeiling:
		h, t := e.ceilingLimits()
		facts["kind"] = over(active, tokens, h, t)
		facts["limit"] = limitText(h, t)
		e.add(sigs, Signal{Detector: "S8", Rule: "s8.session_ceiling", Confidence: 1, Level: L3, Evidence: []int64{ev.Seq}, Facts: facts})
	case tierWarn:
		facts["tier"], facts["kind"], facts["limit"] = tier, over(active, tokens, d.WarnHours, d.WarnTokens), limitText(d.WarnHours, d.WarnTokens)
		e.add(sigs, Signal{Detector: "S8", Rule: "s8.session_long", Confidence: 0.9, Level: L2, Evidence: []int64{ev.Seq}, Facts: facts})
	default:
		facts["tier"], facts["kind"], facts["limit"] = tier, over(active, tokens, d.NoticeHours, d.NoticeTokens), limitText(d.NoticeHours, d.NoticeTokens)
		e.add(sigs, Signal{Detector: "S8", Rule: "s8.session_long", Confidence: 0.9, Level: L0, Evidence: []int64{ev.Seq}, Facts: facts})
	}
}

// ceilingCheck refuses, past the hard limit, every call that changes files,
// runs checks or commands of unknown effect, or starts a subagent. Reading,
// searching and read-only commands stay allowed so the agent can still
// report where it stopped.
func (e *Engine) ceilingCheck(ev *event.Event) *Signal {
	h, t := e.ceilingLimits()
	if h <= 0 && t <= 0 {
		return nil
	}
	active, tokens, _ := e.SessionLength()
	kind := over(active, tokens, h, t)
	if kind == "" || readOnlyCall(ev) {
		return nil
	}
	facts := lengthFacts(active, tokens)
	facts["kind"], facts["limit"], facts["blocked"] = kind, limitText(h, t), true
	return &Signal{Detector: "S8", Rule: "s8.session_ceiling", Confidence: 1, Level: L3, Evidence: []int64{ev.Seq}, Facts: facts}
}

// readOnlyCall reports whether a call only reads.
func readOnlyCall(ev *event.Event) bool {
	switch ev.Tool {
	case event.ToolRead, event.ToolSearch, event.ToolWeb, event.ToolTodo:
		return true
	case event.ToolShell:
		return ev.Category == event.CatExplore && !ev.Unknown && !ev.Mutating
	}
	return false
}

// ExtendCeiling raises the hard limits by what the user granted. It reports
// false when no hard limit is set, so there is nothing to extend.
func (e *Engine) ExtendCeiling(hours float64, tokens int64) (limit string, ok bool) {
	h, t := e.ceilingLimits()
	if h <= 0 && t <= 0 {
		return "", false
	}
	m := e.St.mar
	if h > 0 {
		m.extraHours += hours
	}
	if t > 0 {
		m.extraTok += tokens
	}
	m.tierFired[tierCeiling] = false
	h, t = e.ceilingLimits()
	return limitText(h, t), true
}

func lengthFacts(active time.Duration, tokens int64) map[string]any {
	return map[string]any{"hours": HoursText(active.Hours()), "tokens": TokensText(tokens)}
}

func limitText(hours float64, tokens int64) string {
	switch {
	case hours > 0 && tokens > 0:
		return HoursText(hours) + " 또는 " + TokensText(tokens) + " 토큰"
	case hours > 0:
		return HoursText(hours)
	case tokens > 0:
		return TokensText(tokens) + " 토큰"
	}
	return ""
}

// HoursText renders hours as "3시간 10분".
func HoursText(h float64) string {
	mins := int(h*60 + 0.5)
	switch {
	case mins < 60:
		return fmt.Sprintf("%d분", mins)
	case mins%60 == 0:
		return fmt.Sprintf("%d시간", mins/60)
	}
	return fmt.Sprintf("%d시간 %d분", mins/60, mins%60)
}

// TokensText renders a token count as "12.3M", "850K" or "900".
func TokensText(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dK", n/1_000)
	}
	return fmt.Sprint(n)
}
