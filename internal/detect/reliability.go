package detect

import (
	"fmt"
	"slices"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/policy"
)

const reliabilityRecentLimit = 128
const reliabilityTargetLimit = 64

type reliabilityCandidate struct {
	seq             int64
	waits, failures int
	firstTick, tick uint64
	inputs          map[string]bool
	last            *event.Event
	evidence        []int64
}

type reliabilityState struct {
	background        map[string]bool
	backgroundUnknown bool
	maxSeq            int64
	seen              map[string]bool
	recent            []string
	recentSeq         []int64
	targets           map[string]*reliabilityCandidate
	scope             string
	tick              uint64
	Evicted, Deferred uint64
	reviewEpoch       uint64
}

func newReliabilityState() *reliabilityState {
	return &reliabilityState{background: map[string]bool{}, seen: map[string]bool{}, targets: map[string]*reliabilityCandidate{}}
}

func reliabilityScope(r *event.Reliability) string {
	if r == nil || r.TaskID == "" || r.Revision == 0 || r.AuthorityHash == "" {
		return ""
	}
	return fp.Hash(r.TaskID, fmt.Sprint(r.Revision), r.AuthorityHash)
}

func (e *Engine) ResetReliability() {
	old := e.reliability
	e.reliability = newReliabilityState()
	if old != nil {
		e.reliability.Evicted, e.reliability.Deferred = old.Evicted, old.Deferred
		e.reliability.background, e.reliability.backgroundUnknown, e.reliability.maxSeq = old.background, old.backgroundUnknown, old.maxSeq
	}
	for k := range e.cooldownUntil {
		if strings.HasPrefix(k, "reliability:") {
			delete(e.cooldownUntil, k)
			delete(e.cooldownLevel, k)
		}
	}
}

// ReliabilityStats reports local bounded-state deferrals, not lost-observation
// gaps. Evicting a candidate never invalidates the entire session ledger.
func (e *Engine) ReliabilityStats() (evicted, deferred uint64) {
	return e.reliability.Evicted, e.reliability.Deferred
}

func (e *Engine) reliabilityCandidate(key string, seq int64) *reliabilityCandidate {
	r := e.reliability
	c := r.targets[key]
	if c == nil {
		if len(r.targets) >= reliabilityTargetLimit {
			oldest := ""
			for k, v := range r.targets {
				if oldest == "" || v.seq < r.targets[oldest].seq || (v.seq == r.targets[oldest].seq && k < oldest) {
					oldest = k
				}
			}
			delete(r.targets, oldest)
			for _, rule := range []string{"s1.explicit_waiting", "s8.progress_stall"} {
				delete(e.cooldownUntil, "reliability:"+rule+":"+oldest)
				delete(e.cooldownLevel, "reliability:"+rule+":"+oldest)
			}
			r.Evicted++
			r.Deferred++
		}
		c = &reliabilityCandidate{inputs: map[string]bool{}, firstTick: r.tick}
		r.targets[key] = c
	}
	c.seq = seq
	return c
}

func (e *Engine) reliabilityObservation(ev *event.Event, sigs *[]Signal) {
	state := e.reliability
	if ev.Kind == event.KindTool && ev.ExitCode != nil && *ev.ExitCode != -1 {
		if ev.Background {
			if ev.CallID == "" || len(state.background) >= reliabilityTargetLimit {
				state.backgroundUnknown = true
				state.Deferred++
			} else {
				state.background[ev.CallID] = true
			}
		} else if ev.CallID != "" {
			delete(state.background, ev.CallID)
		}
	}
	if ev.Reliability == nil {
		if wait := event.ExplicitWait(ev); wait != "" {
			ev.Reliability = &event.Reliability{Wait: wait, WaitKind: "sleep", Phase: "result"}
		}
	}
	r := ev.Reliability
	if r != nil && event.ExplicitWait(ev) != "" {
		r.WaitKind = "sleep"
		if r.Wait == "" {
			r.Wait = "explicit"
		}
	}
	if r == nil || ev.Kind != event.KindTool {
		return
	}
	if r.Wait != "" && (len(state.background) > 0 || state.backgroundUnknown) {
		r.Wait = "unknown"
	}
	phase := r.Phase
	if phase == "" {
		if ev.ExitCode == nil {
			phase = "input"
		} else {
			phase = "result"
		}
	}
	identity := ev.CallID
	if identity == "" {
		identity = ev.SourceRef
	}
	if identity == "" || ev.SessionID == "" || r.SourceMissing {
		state.Deferred++
		return
	}
	identity = fp.Hash(ev.SessionID, identity, phase)
	if ev.Seq < state.maxSeq-reliabilityRecentLimit {
		state.Deferred++
		return
	}
	if ev.Seq > state.maxSeq {
		state.maxSeq = ev.Seq
	}
	if state.seen[identity] {
		return
	}
	state.seen[identity] = true
	state.recent = append(state.recent, identity)
	state.recentSeq = append(state.recentSeq, ev.Seq)
	if len(state.recent) > reliabilityRecentLimit {
		delete(state.seen, state.recent[0])
		state.recent = state.recent[1:]
		state.recentSeq = state.recentSeq[1:]
		floor := state.recentSeq[0]
		for _, seq := range state.recentSeq {
			if seq < floor {
				floor = seq
			}
		}
		for key, candidate := range state.targets {
			stale := candidate.seq < floor
			for _, seq := range candidate.evidence {
				if seq < floor {
					stale = true
					break
				}
			}
			if stale {
				delete(state.targets, key)
				state.Evicted++
				state.Deferred++
				for _, rule := range []string{"s1.explicit_waiting", "s8.progress_stall"} {
					delete(e.cooldownUntil, "reliability:"+rule+":"+key)
					delete(e.cooldownLevel, "reliability:"+rule+":"+key)
				}
			}
		}
	}
	if phase == "input" {
		return
	}
	scope := reliabilityScope(r)
	if scope != "" {
		scope = fp.Hash(e.Session, scope)
	}
	if scope != "" && scope != state.scope && r.Complete {
		e.ResetReliability()
		state = e.reliability
		state.scope = scope
		state.seen[identity] = true
		state.recent = append(state.recent, identity)
		state.recentSeq = append(state.recentSeq, ev.Seq)
	}
	if r.Complete && scope != "" {
		state.tick++
	}
	e.explicitWaiting(ev, scope, sigs)
	e.progressStall(ev, scope, sigs)
}

func reliabilityCooldown(s Signal) string {
	if policy.AdviceOnly(s.Rule) {
		key, _ := s.Facts["candidate_key"].(string)
		return "reliability:" + s.Rule + ":" + key
	}
	return s.Rule
}

func (e *Engine) RestoreReliabilityVerdicts(vs []Signal) {
	for _, s := range vs {
		if !policy.AdviceOnly(s.Rule) || !s.Primary || s.Suppressed || s.Level == L0 {
			continue
		}
		candidate, _ := s.Facts["candidate_key"].(string)
		if _, ok := e.reliability.targets[candidate]; !ok {
			continue
		}
		key := reliabilityCooldown(s)
		e.cooldownUntil[key] = s.Seq + int64(e.Cfg.Levels.NudgeCooldownEvents)
		e.cooldownLevel[key] = L1
	}
}

func reliabilityShadow(e *Engine, s Signal) bool {
	return policy.AdviceOnly(s.Rule) && (e.Cfg.Rollout.Mode == "shadow" || slices.Contains(e.Cfg.Rollout.ShadowRules, s.Rule))
}

// ObserveReliabilityFact records zero-cost harness check evidence without
// feeding synthetic runs into existing S1/S2 or their global progress state.
func (e *Engine) ObserveReliabilityFact(ev *event.Event) []Signal {
	e.ObserveWatch(ev)
	var sigs []Signal
	e.reliabilityObservation(ev, &sigs)
	if e.Restoring {
		return nil
	}
	return e.combine(ev, sigs)
}

func slicesDeleteReliability(sigs []Signal) []Signal {
	out := sigs[:0]
	for _, s := range sigs {
		if !policy.AdviceOnly(s.Rule) {
			out = append(out, s)
		}
	}
	return out
}
