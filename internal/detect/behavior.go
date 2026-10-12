package detect

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
)

type behaviorEntry struct {
	seqs        []int64
	environment string
	input       string
	selection   string
	context     string
	timeout     int64
	result      string
	commands    map[string]bool
}

type behaviorState struct {
	entries   map[string]*behaviorEntry
	knowledge map[string]bool
	scope     string
}

func (e *Engine) deferCandidate(ev *event.Event, rule, code string) {
	for _, d := range ev.Decisions {
		if d.Rule == rule && d.Code == code {
			return
		}
	}
	ev.Decisions = append(ev.Decisions, event.Decision{Rule: rule, Code: code, Evidence: []int64{ev.Seq}})
}

func sortedHash(values []string) string {
	v := append([]string(nil), values...)
	sort.Strings(v)
	return fp.Hash(strings.Join(v, "\x00"))
}

// BehaviorTarget relates a completed action to a failure/review target. It is
// an advisory identity; it must never replace ExecFP for execution blocking.
func BehaviorTarget(ev *event.Event) string {
	if ev.BehaviorTarget != "" {
		return ev.BehaviorTarget
	}
	if r := ev.Reliability; r != nil && r.CheckID != "" {
		return fp.Hash("check", r.TaskID, r.CheckID)
	}
	if len(ev.FailedTests) > 0 {
		return fp.Hash("tests", sortedHash(ev.FailedTests))
	}
	if len(ev.ErrFPs) > 0 {
		return fp.Hash("errors", sortedHash(ev.ErrFPs))
	}
	if ev.Purpose != "" {
		return fp.Hash("purpose", ev.Purpose, sortedHash(ev.Paths))
	}
	if len(ev.Paths) > 0 {
		return fp.Hash("paths", ev.Tool, sortedHash(ev.Paths))
	}
	if ev.CmdFP != "" {
		return fp.Hash("command", ev.CmdFP)
	}
	return ""
}

func (e *Engine) observeBehavior(ev *event.Event, sigs *[]Signal) {
	if e.Restoring {
		original := ev.WorkProgress
		decisions := append([]event.Decision(nil), ev.Decisions...)
		defer func() { ev.WorkProgress = original; ev.Decisions = decisions }()
	}
	scope := fp.Hash(e.FirstPrompt)
	if r := ev.Reliability; r != nil {
		scope = fp.Hash(scope, r.TaskID, fmt.Sprint(r.Revision))
	}
	if e.behavior == nil || e.behavior.scope != scope {
		e.behavior = &behaviorState{scope: scope, entries: map[string]*behaviorEntry{}, knowledge: map[string]bool{}}
	}
	b := e.behavior
	if ev.Kind == event.KindPrompt && !ev.Forced {
		b.entries = map[string]*behaviorEntry{}
		return
	}
	if ev.Kind != event.KindTool || ev.Category == event.CatWatch {
		return
	}
	ev.WorkProgress.Activity = ev.Category == event.CatProduce && !ev.IsError
	if ev.WorkProgress.Activity {
		ev.WorkProgress.Basis = "observed_file_activity"
	}
	if ev.Tool == event.ToolShell && ev.Category == event.CatVerify {
		for _, item := range []struct {
			missing bool
			code    string
		}{{ev.ExitCode == nil, "missing_exit"}, {ev.WSBefore == "", "missing_ws"}, {ev.ResultFP == "", "missing_result"}} {
			if item.missing {
				e.deferCandidate(ev, "s1.identical_rerun", item.code)
			}
		}
	}
	if ev.Background {
		e.deferCandidate(ev, "s2.behavior_loop", "background_pending")
		return
	}
	if ev.Unknown || ev.Mutating && ev.Category != event.CatProduce && ev.Category != event.CatVerify {
		b.entries = map[string]*behaviorEntry{}
		e.deferCandidate(ev, "s2.behavior_loop", "environment_unknown")
		return
	}
	if ev.Tool == event.ToolShell && ev.Cmd != "" && ev.BehaviorContext == "" {
		ctx, certain := fp.ExecutionContext(ev.Cmd, "")
		if certain {
			ev.BehaviorContext = fp.Hash(ctx, ev.Dir)
		} else {
			e.deferCandidate(ev, "s2.behavior_loop", "execution_context_unknown")
			for key := range b.entries {
				if strings.HasPrefix(key, "failure:") {
					delete(b.entries, key)
				}
			}
			return
		}
	}
	if ExecutedVerify(ev) {
		ev.BehaviorSelection = e.behaviorSelection(ev)
	}
	result := ev.BehaviorResult
	if result == "" {
		result = ev.ResultFP
	}
	if ev.Category == event.CatExplore && !ev.IsError {
		if ev.ObservationFP != "" {
			result = ev.ObservationFP
		} else if ev.Text != "" {
			result = fp.Hash(ev.Text)
		}
	}
	target := BehaviorTarget(ev)
	if ev.Category == event.CatExplore && !IsReview(ev) && intent.ResearchRequest(e.FirstPrompt) && result != "" {
		target = fp.Hash("research-result", result)
	}
	if target == "" {
		return
	}
	ev.BehaviorTarget = target
	ev.BehaviorResult = result
	if ev.Category == event.CatExplore && result != "" && !ev.IsError {
		k := fp.Hash("observed-result", result)
		if !b.knowledge[k] {
			if len(b.knowledge) >= 512 {
				clear(b.knowledge)
			}
			b.knowledge[k] = true
			ev.WorkProgress.Knowledge = true
			ev.WorkProgress.Basis = "new_observed_result"
		}
	}
	if ExecutedVerify(ev) && !failing(ev) {
		ev.WorkProgress.Verification = true
		ev.WorkProgress.Basis = "completed_passing_check"
		delete(b.entries, "failure:"+target)
		selection := e.behaviorSelection(ev)
		for key, ent := range b.entries {
			if strings.HasPrefix(key, "failure:") && ent.selection == selection {
				delete(b.entries, key)
			}
		}
		return
	}
	rule, key, threshold := "", "", 3
	switch {
	case ExecutedVerify(ev) && failing(ev) && (len(ev.FailedTests) > 0 || len(ev.ErrFPs) > 0 || ev.Reliability != nil && ev.Reliability.CheckID != ""):
		rule, key = "s2.behavior_loop", "failure:"+target
	case ev.Category == event.CatExplore && !IsReview(ev) && intent.ResearchRequest(e.FirstPrompt) && result != "" && !ev.IsError:
		rule, key = "s4.research_repeat", "research:"+target
	case IsReview(ev) && result != "" && !ev.IsError:
		rule, key = "s1.delegation_loop", "review:"+target
		threshold = 3
	default:
		if IsReview(ev) && result == "" {
			e.deferCandidate(ev, "s1.delegation_loop", "missing_result")
		}
		return
	}
	if e.nondeterministic(ev.CmdNorm) || ev.Reliability != nil && ev.Reliability.Flaky {
		delete(b.entries, key)
		e.deferCandidate(ev, rule, "flaky")
		return
	}
	env, input := "", ""
	if ev.Reliability != nil {
		env = ev.Reliability.EnvironmentHash
		input = ev.Reliability.InputHash
	}
	selection := ""
	if rule == "s2.behavior_loop" {
		selection = e.behaviorSelection(ev)
	}
	ent := b.entries[key]
	if ent != nil && (ent.context != ev.BehaviorContext || ent.timeout != ev.TimeoutMS || ent.selection != selection || ent.environment != env || ent.input != input || rule != "s2.behavior_loop" && ent.result != result || rule == "s2.behavior_loop" && ent.result != "" && result != "" && ent.result != result) {
		delete(b.entries, key)
		ent = nil
		e.deferCandidate(ev, rule, "new_evidence")
	}
	if ent == nil {
		if len(b.entries) >= 128 {
			clear(b.entries)
			e.deferCandidate(ev, rule, "candidate_limit")
		}
		ent = &behaviorEntry{environment: env, input: input, selection: selection, context: ev.BehaviorContext, timeout: ev.TimeoutMS, result: result, commands: map[string]bool{}}
		b.entries[key] = ent
	}
	if len(ent.commands) >= 12 {
		clear(ent.commands)
	}
	ent.commands[ev.CmdFP] = true
	ent.seqs = append(ent.seqs, ev.Seq)
	if len(ent.seqs) > 12 {
		ent.seqs = ent.seqs[len(ent.seqs)-12:]
	}
	if len(ent.seqs) < threshold {
		return
	}
	if env == "" {
		e.deferCandidate(ev, rule, "environment_unknown")
	}
	// Identical executions remain the existing rule's responsibility.
	if rule == "s2.behavior_loop" && len(ent.commands) < 2 && ev.WSBefore != "" {
		first := e.St.bySeq[ent.seqs[0]]
		if first != nil && first.WSBefore == ev.WSBefore {
			return
		}
	}
	e.add(sigs, Signal{Detector: strings.ToUpper(strings.Split(rule, ".")[0]), Rule: rule, Level: L1, Confidence: 0.65, Estimate: true, Evidence: append([]int64(nil), ent.seqs...), Facts: map[string]any{"target_hash": target, "count": len(ent.seqs), "environment_known": env != "", "kind": "advisory_similarity"}})
}

func (e *Engine) behaviorSelection(ev *event.Event) string {
	if ev.BehaviorSelection != "" {
		return ev.BehaviorSelection
	}
	if ev.Reliability != nil && ev.Reliability.CheckID != "" {
		return fp.Hash("check", ev.Reliability.CheckID)
	}
	checks := e.checkOf(ev)
	var identities []string
	for _, cmd := range checks.tests {
		identities = append(identities, classify.CheckSelection(cmd))
	}
	if len(identities) > 0 {
		return sortedHash(identities)
	}
	return classify.CheckSelection(cmdOf(ev))
}
