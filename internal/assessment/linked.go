package assessment

import (
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"sort"
)

type TaskNode struct {
	Link   intent.SessionLink
	Events []*event.Event
}
type LinkedEvidence struct {
	Agent     string `json:"agent"`
	SessionID string `json:"session_id"`
	Seq       int64  `json:"seq"`
}

type LinkedCycle struct {
	HandoffReturn []LinkedEvidence `json:"handoff_return,omitempty"`
	Result        string           `json:"result_hash"`
	Evidence      []LinkedEvidence `json:"evidence"`
	KeyHash       string           `json:"key_hash"`
	Target        string           `json:"target_hash"`
	Sessions      []string         `json:"sessions"`
	Count         int              `json:"count"`
	Estimate      bool             `json:"estimate"`
}
type LinkedSummary struct {
	UsageScope      string        `json:"usage_scope"`
	UsageObserved   bool          `json:"usage_observed"`
	WindowTruncated bool          `json:"window_truncated,omitempty"`
	State           string        `json:"state"`
	Unknown         int           `json:"unknown_observations"`
	Eligible        int           `json:"eligible_observations"`
	TaskID          string        `json:"task_id"`
	Sessions        int           `json:"linked_sessions"`
	Counted         int           `json:"counted_sessions"`
	Excluded        int           `json:"merged_children_excluded"`
	Cycles          []LinkedCycle `json:"cycles"`
	UsageTokens     int64         `json:"usage_tokens"`
}

// LinkedTask uses explicit lineage only. A parent's includes_children flag
// excludes its descendants; hashes alone never merge different sessions/tasks.
func LinkedTask(nodes []TaskNode) (LinkedSummary, error) {
	out := LinkedSummary{UsageScope: "observed_events", Cycles: []LinkedCycle{}}
	by := map[string]TaskNode{}
	key := func(a, s string) string { return a + "/" + s }
	for _, n := range nodes {
		if n.Link.TaskID == "" || n.Link.Agent == "" || n.Link.SessionID == "" {
			return out, fmt.Errorf("incomplete task link")
		}
		if out.TaskID != "" && out.TaskID != n.Link.TaskID {
			return out, fmt.Errorf("mixed task identities")
		}
		out.TaskID = n.Link.TaskID
		k := key(n.Link.Agent, n.Link.SessionID)
		if _, ok := by[k]; ok {
			return out, fmt.Errorf("duplicate session link")
		}
		by[k] = n
	}
	out.Sessions = len(nodes)
	type observed struct {
		link intent.SessionLink
		ev   *event.Event
	}
	var observations []observed
	knownTime := map[string]bool{}
	refKey := func(a, s string, seq int64) string { return fmt.Sprintf("%s/%s/%d", a, s, seq) }
	for _, n := range nodes {
		seen := map[string]bool{}
		cur := n
		excluded := false
		for cur.Link.ParentSessionID != "" {
			k := key(cur.Link.ParentAgent, cur.Link.ParentSessionID)
			if seen[k] || k == key(n.Link.Agent, n.Link.SessionID) {
				return out, fmt.Errorf("cyclic session lineage")
			}
			seen[k] = true
			parent, ok := by[k]
			if !ok {
				break
			}
			if parent.Link.IncludesChildren {
				excluded = true
			}
			cur = parent
		}
		if excluded {
			out.Excluded++
			continue
		}
		out.Counted++
		for _, ev := range n.Events {
			if ev == nil {
				return out, fmt.Errorf("nil linked observation")
			}
			knownTime[refKey(n.Link.Agent, n.Link.SessionID, ev.Seq)] = !ev.TS.IsZero()
			out.UsageTokens += ev.Usage.Total()
			out.UsageObserved = out.UsageObserved || ev.Usage.Total() > 0
			if ev.Kind == event.KindTool && (ev.Category == event.CatVerify || ev.Category == event.CatExplore) {
				observations = append(observations, observed{n.Link, ev})
			}
		}
	}
	sort.SliceStable(observations, func(i, j int) bool {
		a, b := observations[i], observations[j]
		if !a.ev.TS.Equal(b.ev.TS) {
			return a.ev.TS.Before(b.ev.TS)
		}
		if key(a.link.Agent, a.link.SessionID) != key(b.link.Agent, b.link.SessionID) {
			return key(a.link.Agent, a.link.SessionID) < key(b.link.Agent, b.link.SessionID)
		}
		return a.ev.Seq < b.ev.Seq
	})
	type repeat struct {
		refs                    []LinkedEvidence
		sessions                map[string]bool
		result, target, keyHash string
	}
	groups := map[string]*repeat{}
	unorderedPass := map[string]bool{}
	for _, o := range observations {
		ev, r := o.ev, o.ev.Reliability
		if ev.BehaviorTarget == "" || ev.BehaviorResult == "" || ev.Background || r == nil || r.TaskID != out.TaskID || r.Revision == 0 || !r.Complete || r.EnvironmentHash == "" || r.InputHash == "" || r.Flaky || r.SourceMissing {
			out.Unknown++
			continue
		}
		base := fmt.Sprintf("%d/%s/%s/%s/%s/%s", r.Revision, ev.BehaviorTarget, r.EnvironmentHash, r.InputHash, ev.BehaviorContext, r.AuthorityHash)
		if ev.Category == event.CatVerify {
			if ev.ExitCode == nil || *ev.ExitCode < 0 {
				out.Unknown++
				continue
			}
			if *ev.ExitCode == 0 && !ev.IsError && len(ev.FailedTests) == 0 && len(ev.ErrFPs) == 0 {
				delete(groups, base)
				if ev.TS.IsZero() {
					unorderedPass[base] = true
				}
				continue
			}
		}
		if unorderedPass[base] {
			out.Unknown++
			continue
		}
		out.Eligible++
		g := groups[base]
		if g == nil || g.result != ev.BehaviorResult {
			g = &repeat{sessions: map[string]bool{}, result: ev.BehaviorResult, target: ev.BehaviorTarget, keyHash: fp.Hash(base, ev.BehaviorResult)}
			groups[base] = g
		}
		g.refs = append(g.refs, LinkedEvidence{Agent: o.link.Agent, SessionID: o.link.SessionID, Seq: ev.Seq})
		g.sessions[key(o.link.Agent, o.link.SessionID)] = true
	}
	for _, g := range groups {
		if len(g.refs) >= 3 && len(g.sessions) >= 2 {
			c := LinkedCycle{Result: g.result, Evidence: g.refs, KeyHash: g.keyHash, Target: g.target, Count: len(g.refs), Estimate: true}
			for id := range g.sessions {
				c.Sessions = append(c.Sessions, id)
			}
			for _, current := range nodes {
				parent, ok := by[key(current.Link.ParentAgent, current.Link.ParentSessionID)]
				if !ok {
					continue
				}
				grandparent, ok := by[key(parent.Link.ParentAgent, parent.Link.ParentSessionID)]
				if !ok || grandparent.Link.Agent != current.Link.Agent || parent.Link.Agent == current.Link.Agent {
					continue
				}
				var ancestor, via *LinkedEvidence
				for i := range g.refs {
					ref := g.refs[i]
					known := knownTime[refKey(ref.Agent, ref.SessionID, ref.Seq)]
					if !known {
						continue
					}
					switch key(ref.Agent, ref.SessionID) {
					case key(grandparent.Link.Agent, grandparent.Link.SessionID):
						cp := ref
						ancestor = &cp
						via = nil
					case key(parent.Link.Agent, parent.Link.SessionID):
						if ancestor != nil {
							cp := ref
							via = &cp
						}
					case key(current.Link.Agent, current.Link.SessionID):
						if ancestor != nil && via != nil {
							c.HandoffReturn = []LinkedEvidence{*ancestor, *via, ref}
						}
					}
					if len(c.HandoffReturn) > 0 {
						break
					}
				}
				if len(c.HandoffReturn) > 0 {
					break
				}
			}

			sort.Strings(c.Sessions)
			out.Cycles = append(out.Cycles, c)
		}
	}

	sort.Slice(out.Cycles, func(i, j int) bool { return out.Cycles[i].KeyHash < out.Cycles[j].KeyHash })
	out.State = "no_repeat_candidate"
	if out.Eligible == 0 {
		out.State = "insufficient_evidence"
	}
	if len(out.Cycles) > 0 {
		out.State = "candidate"
	}
	return out, nil
}
