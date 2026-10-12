// Package assessment separates behavior findings, cost attribution and
// observable evidence. It does not grant authority or infer completeness.
package assessment

import (
	"fmt"
	"regexp"
	"slices"
	"sort"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/policy"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
)

type Coverage struct {
	Gaps                  []Gap  `json:"affected_intervals,omitempty"`
	ShellCalls            int    `json:"shell_calls"`
	CompletedShellResults int    `json:"completed_shell_results"`
	WorkspaceKnown        int    `json:"workspace_known"`
	ResultKnown           int    `json:"result_known"`
	TotalCollection       string `json:"total_collection"`
}

type Summary struct {
	Requirements    []event.RequirementProgress `json:"requirements,omitempty"`
	RolloutBasis    string                      `json:"rollout_basis"`
	State           string                      `json:"state"`
	Confirmed       int                         `json:"confirmed"`
	Candidates      int                         `json:"candidates"`
	Findings        int                         `json:"findings"`
	Shadow          int                         `json:"shadow"`
	CostBasis       string                      `json:"cost_basis"`
	WasteRatio      string                      `json:"waste_ratio"`
	CostAttribution string                      `json:"cost_attribution"`
	Completion      string                      `json:"completion"`
	Coverage        Coverage                    `json:"coverage"`
	Reasons         map[string]int              `json:"reasons"`
	Progress        event.WorkProgress          `json:"progress"`
}

var reasonIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,63}$`)

func Build(evs []*event.Event, vs []detect.Signal, cfg config.Config, met, total int) Summary {
	s := Summary{RolloutBasis: "current_configuration", State: "no_finding", CostBasis: "unknown", WasteRatio: "미확인", CostAttribution: "unknown", Completion: "unknown", Reasons: map[string]int{}, Coverage: Coverage{TotalCollection: "unknown"}}
	var tokens, unpriced, micro, waste, wasteTokens int64
	var gaps GapTracker
	for _, ev := range evs {
		gaps.Observe(ev)
		if len(ev.Requirements) > 0 {
			s.Requirements = ev.Requirements
		}
		tokens += ev.Usage.Total()
		micro += ev.CostMicroKRW
		if !ev.Priced {
			unpriced += ev.Usage.Total()
		}
		if ev.Bucket == "waste" || ev.Symptom != "" {
			waste += ev.CostMicroKRW
			wasteTokens += ev.Usage.Total()
			if !ev.Estimated {
				s.Confirmed++
			}
		}
		if ev.Kind == event.KindTool && ev.Tool == event.ToolShell && !ev.Background {
			s.Coverage.ShellCalls++
			if ev.ExitCode != nil && *ev.ExitCode != -1 {
				s.Coverage.CompletedShellResults++
			}
			if ev.WSBefore != "" {
				s.Coverage.WorkspaceKnown++
			}
			if ev.ResultFP != "" {
				s.Coverage.ResultKnown++
			}
		}
		codes := map[string]bool{}
		for _, d := range ev.Decisions {
			if reasonIdentifier.MatchString(d.Code) && d.Code != "shadow" && d.Code != "cooldown" {
				codes[d.Code] = true
			}
		}
		if ev.Kind == event.KindTool && ev.Tool == event.ToolShell && !ev.Background {
			if ev.ExitCode == nil || *ev.ExitCode == -1 {
				codes["missing_exit"] = true
			}
			if ev.WSBefore == "" {
				codes["missing_ws"] = true
			}
			if ev.ResultFP == "" {
				codes["missing_result"] = true
			}
		}
		for code := range codes {
			s.Reasons[code]++
		}
		s.Progress.Activity = s.Progress.Activity || ev.WorkProgress.Activity
		s.Progress.Knowledge = s.Progress.Knowledge || ev.WorkProgress.Knowledge
		s.Progress.Verification = s.Progress.Verification || ev.WorkProgress.Verification
	}
	s.Coverage.Gaps = gaps.Snapshot()
	seen := map[string]bool{}
	for _, v := range vs {
		key := fmt.Sprintf("%s:%d", v.Rule, v.Seq)
		if seen[key] {
			continue
		}
		seen[key] = true
		s.Findings++
		if v.Estimate {
			s.Candidates++
		}
		if (cfg.Rollout.Mode == "shadow" && !policy.ExplicitGuardrail(v.Rule)) || slices.Contains(cfg.Rollout.ShadowRules, v.Rule) || rulepack.IsLearned(v.Rule) && !slices.Contains(cfg.Rollout.RecommendRules, v.Rule) {
			s.Shadow++
			s.Reasons["shadow"]++
		}
		if v.Suppressed {
			s.Reasons["cooldown"]++
		}
	}
	s.ApplyMeasurement(tokens, unpriced, micro, waste, wasteTokens, met, total)
	return s
}

func (s Summary) Text() string {
	label := map[string]string{"no_finding": "탐지 없음", "confirmed_waste": "확정 낭비 있음", "candidate": "확인 후보 있음", "finding_without_cost": "비용 미귀속 지적 있음", "insufficient_evidence": "판단 자료 부족"}[s.State]
	return fmt.Sprintf("%s · 확정 사건 %d · 확인 후보 %d · 판정 %d · 기록 전용 %d · 낭비 분류율 %s (%s)", label, s.Confirmed, s.Candidates, s.Findings, s.Shadow, s.WasteRatio, s.CostBasis)
}

func (s Summary) ReasonKeys() []string {
	keys := make([]string, 0, len(s.Reasons))
	for k := range s.Reasons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ApplyMeasurement refreshes late usage and current completion independently of
// immutable observations. A missing billable denominator remains unknown.
func (s *Summary) ApplyMeasurement(tokens, unpriced, micro, waste, wasteTokens int64, met, total int) {
	s.State = "no_finding"
	s.CostBasis = "unknown"
	s.WasteRatio = "미확인"
	s.CostAttribution = "unknown"
	s.Completion = "unknown"
	s.Progress.Completion = false
	delete(s.Reasons, "completion_unobserved")
	delete(s.Reasons, "usage_unobserved")
	if total > 0 {
		s.Completion = "pending"
		if met == total {
			s.Completion = "checks_passed"
			s.Progress.Completion = true
		}
	} else {
		s.Reasons["completion_unobserved"]++
	}
	if tokens > 0 {
		s.CostBasis = "tokens"
		s.WasteRatio = cost.RatioText(wasteTokens, tokens)
		if unpriced == 0 && micro > 0 {
			s.CostBasis = "money"
			s.WasteRatio = cost.RatioText(waste, micro)
		}
		s.CostAttribution = "observed"
		if s.Findings > 0 && wasteTokens == 0 {
			s.CostAttribution = "unattributed_findings"
		}
	} else {
		s.Reasons["usage_unobserved"]++
	}
	switch {
	case s.Confirmed > 0:
		s.State = "confirmed_waste"
	case s.Candidates > 0:
		s.State = "candidate"
	case s.Findings > 0:
		s.State = "finding_without_cost"
	case s.Coverage.ShellCalls > s.Coverage.CompletedShellResults || tokens == 0:
		s.State = "insufficient_evidence"
	}
}

// BuildRecorded uses persisted delivery decisions, not today's rollout config.
// A historical record without delivery metadata stays unknown.
func BuildRecorded(evs []*event.Event, vs []detect.Signal, channels map[string]string) Summary {
	cfg := config.Default()
	cfg.Rollout.ShadowRules = nil
	s := Build(evs, vs, cfg, 0, 0)
	s.RolloutBasis = "recorded"
	s.Shadow = 0
	delete(s.Reasons, "shadow")
	recorded := map[string]bool{}
	for _, ev := range evs {
		for _, d := range ev.Decisions {
			if d.Code == "shadow" {
				recorded[fmt.Sprintf("%s:%d", d.Rule, ev.Seq)] = true
			}
		}
	}
	seen := map[string]bool{}
	for _, v := range vs {
		key := fmt.Sprintf("%s:%d", v.Rule, v.Seq)
		if seen[key] {
			continue
		}
		seen[key] = true
		channel := channels[v.ID]
		if recorded[key] || channel == "shadow" {
			s.Shadow++
			s.Reasons["shadow"]++
		} else if channel == "" && v.Primary && !v.Suppressed && v.Level > detect.L0 {
			s.Reasons["rollout_unrecorded"]++
		}
	}
	return s
}
