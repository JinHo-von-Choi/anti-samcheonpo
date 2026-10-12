// Package alignment links requested outcomes, agent claims and observed results.
// Self-reports and semantic links never become execution authority or completion.
package alignment

import (
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"regexp"
	"strings"
)

type Requirement struct {
	ID       string  `json:"id"`
	Label    string  `json:"label"`
	Source   string  `json:"source"`
	Status   string  `json:"status"`
	Evidence []int64 `json:"evidence,omitempty"`
}
type Observation struct {
	ID            int64  `json:"id"`
	Role          string `json:"role"`
	Claim         string `json:"claim,omitempty"`
	RequirementID string `json:"requirement_id,omitempty"`
	Result        string `json:"result,omitempty"`
	Signature     string `json:"signature,omitempty"`
}
type Graph struct {
	Requirements []Requirement `json:"requirements"`
	Observations []Observation `json:"observations"`
	Completion   string        `json:"completion"`
}

var completionWords = regexp.MustCompile(`(?i)(완료|끝났|구현했|done|completed|finished)`)
var partialWords = regexp.MustCompile(`(?i)(부분|일부|미완료|아직|남았|예정|초안|완료하지|완료되지|끝나지|못했|partial|draft|remaining|not complete|not done|not finished|incomplete|unfinished)`)
var scopedCompletionWords = regexp.MustCompile(`(?i)(검사|테스트|빌드|checkpoint|check|test|build).*(완료|끝났|done|completed|finished)`)
var workCompletionWords = regexp.MustCompile(`(?i)(작업|구현|기능|요구사항|task|implementation|requirements)`)
var permissionWords = regexp.MustCompile(`(?i)(진행할까요|계속할까요|해도 될까요|승인.*[?？]|허락.*[?？]|shall I|should I|may I|permission|approve)`)
var externalWords = regexp.MustCompile(`(?i)(배포|운영|푸시|push|deploy|publish|전송|송금|delete|삭제)`)
var actionWords = regexp.MustCompile(`(?i)(구현|진행|수정|작성|고쳐|시작|implement|fix|write|start|continue)`)

func clipped(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

func Requirements(goal string, c *contract.Contract, accepted bool) []Requirement {
	var out []Requirement
	for _, constraint := range Constraints(goal) {
		out = append(out, Requirement{ID: constraint.ID, Label: constraint.Label, Source: "explicit_request_constraint", Status: "unknown"})
	}
	if c != nil && len(c.Done) > 0 {
		source := "draft_contract"
		if accepted {
			source = "accepted_contract"
		}
		for _, check := range c.Done {
			label := check.Manual
			if label == "" {
				label = check.Check
			}
			out = append(out, Requirement{ID: check.ID, Label: clipped(label, 160), Source: source, Status: "unknown"})
			if len(out) == 8 {
				break
			}
		}
		return out
	}
	seen := map[string]bool{}
	for _, line := range regexp.MustCompile(`\n|[;；]|(?:그리고|하고 나서|한 뒤)`).Split(goal, -1) {
		line = strings.TrimLeft(strings.TrimSpace(line), "-*0123456789. ")
		if line == "" {
			continue
		}
		id := "request_" + fp.Hash(line)[:12]
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, Requirement{ID: id, Label: clipped(line, 160), Source: "inferred_request", Status: "unknown"})
		if len(out) == 8 {
			break
		}
	}
	return out
}

func Build(goal string, c *contract.Contract, accepted bool, events []*event.Event, boundary *event.Reliability) Graph {
	g := Graph{Requirements: Requirements(goal, c, accepted), Completion: "unknown"}
	by := map[string]int{}
	for i, r := range g.Requirements {
		by[r.ID] = i
	}
	if len(events) > 64 {
		events = events[len(events)-64:]
	}
	latestRequest := int64(-1)
	for _, ev := range events {
		if ev.Kind == event.KindPrompt && ev.PromptOrigin == "user" {
			latestRequest = ev.Seq
		}
	}
	for _, ev := range events {
		if ev.Category == event.CatWatch {
			continue
		}
		o := Observation{ID: ev.Seq}
		switch ev.Kind {
		case event.KindPrompt:
			if ev.PromptOrigin != "user" || ev.Text == "" || ev.Seq != latestRequest {
				continue
			}
			if accepted && boundary != nil && boundary.AuthorityHash != "" && ev.Summary == "explicit intent command" {
				o.Role = "accepted_confirmation"
			} else if !actionWords.MatchString(ev.Text) {
				continue
			}
			if o.Role == "" {
				o.Role = "user_request"
			}
		case event.KindMessage:
			if ev.Seq < latestRequest {
				continue
			}
			o.Role = "agent_claim"
			prose := claimProse(ev.Text)
			o.Signature = fp.Hash(prose)
			switch {
			case permissionWords.MatchString(prose):
				o.Claim = "permission_request"
				if externalWords.MatchString(prose) && !externalWords.MatchString(goal) {
					o.Claim = "new_scope_permission"
				}
			case completionWords.MatchString(prose) && !partialWords.MatchString(prose):
				o.Claim = "full_completion"
				if scopedCompletionWords.MatchString(prose) && !workCompletionWords.MatchString(prose) {
					o.Claim = "scoped_completion"
				}
			case partialWords.MatchString(prose):
				o.Claim = "partial_disclosure"
			default:
				continue
			}
		case event.KindTool:
			o.Role = "tool_result"
			o.Signature = ev.BehaviorResult
			for _, change := range ev.RequirementChanges {
				if i, ok := by[change.RequirementID]; ok {
					g.Requirements[i].Status = "violation_candidate"
					g.Requirements[i].Evidence = []int64{ev.Seq}
					g.Observations = append(g.Observations, Observation{ID: ev.Seq, Role: "tool_result", RequirementID: change.RequirementID, Result: "requirement_change", Signature: change.PathHash})
				}
			}
			if (ev.Category == event.CatProduce || ev.Mutating || ev.Unknown) && !ev.IsError {
				o.Result = "artifact_activity"
				for i := range g.Requirements {
					if g.Requirements[i].Status == "passing_evidence_candidate" || g.Requirements[i].Status == "failure_observed" {
						g.Requirements[i].Status = "inputs_changed"
						g.Requirements[i].Evidence = []int64{ev.Seq}
					}
				}
			}
			r := ev.Reliability
			if ev.Category == event.CatVerify && ev.ExitCode != nil && !ev.Background {
				if r == nil || r.CheckID == "" {
					for i := range g.Requirements {
						if g.Requirements[i].Status == "failure_observed" {
							g.Requirements[i].Status = "verification_unlinked"
							g.Requirements[i].Evidence = []int64{ev.Seq}
						}
					}
				}
				o.Result = "check_failed"
				if *ev.ExitCode == 0 && !ev.IsError && len(ev.FailedTests) == 0 {
					o.Result = "check_passed"
				}
				if *ev.ExitCode < 0 {
					o.Result = "unknown"
				}
				if accepted && r != nil && boundary != nil && r.Complete && !r.Flaky && !r.SourceMissing && r.TaskID == boundary.TaskID && r.Revision == boundary.Revision && r.AuthorityHash != "" && r.AuthorityHash == boundary.AuthorityHash && r.CheckID != "" && r.InputHash != "" && r.EnvironmentHash != "" {
					if i, ok := by[r.CheckID]; ok {
						o.RequirementID = r.CheckID
						g.Requirements[i].Status = "failure_observed"
						if o.Result == "check_passed" {
							g.Requirements[i].Status = "passing_evidence_candidate"
						}
						if o.Result == "unknown" {
							g.Requirements[i].Status = "unknown"
						}
						g.Requirements[i].Evidence = []int64{ev.Seq}
					}
				}
			}
			if o.Result == "" && ev.BehaviorResult != "" {
				o.Result = "observed_result"
			}
			if o.Result == "" {
				continue
			}
		default:
			continue
		}
		g.Observations = append(g.Observations, o)
	}
	if len(g.Observations) > 24 {
		g.Observations = g.Observations[len(g.Observations)-24:]
	}
	return g
}

// ValidIssue checks relation roles and outcomes rather than mere ID membership.
func (g Graph) ValidIssue(kind string, requirements []string, claims, results, authorizations []int64) bool {
	req := map[string]Requirement{}
	for _, r := range g.Requirements {
		req[r.ID] = r
	}
	obs := map[int64]Observation{}
	for _, o := range g.Observations {
		obs[o.ID] = o
	}
	if len(requirements) == 0 || len(requirements) > 8 || len(claims) == 0 || len(claims) > 8 || len(results) > 8 || len(authorizations) > 8 {
		return false
	}
	for _, id := range requirements {
		if _, ok := req[id]; !ok {
			return false
		}
	}
	for _, id := range claims {
		o, ok := obs[id]
		if !ok || o.Role != "agent_claim" {
			return false
		}
		if kind == "repeated_permission" {
			if o.Claim != "permission_request" {
				return false
			}
		} else if o.Claim != "full_completion" {
			return false
		}
	}
	if kind == "repeated_permission" {
		if len(authorizations) == 0 {
			return false
		}
		for _, id := range authorizations {
			if obs[id].Role != "user_request" && obs[id].Role != "accepted_confirmation" {
				return false
			}
			for _, claim := range claims {
				if id >= claim {
					return false
				}
			}
		}
		return true
	}
	if len(results) == 0 {
		return false
	}
	linked := false
	for _, id := range results {
		o, ok := obs[id]
		if !ok || o.Role != "tool_result" {
			return false
		}
		for _, r := range requirements {
			if o.RequirementID == r && o.Result == "check_failed" && req[r].Status == "failure_observed" && len(req[r].Evidence) == 1 && req[r].Evidence[0] == id {
				linked = true
			}
		}
		for _, claim := range claims {
			if id >= claim {
				return false
			}
		}
	}
	return linked
}
func (g Graph) Progress() []event.RequirementProgress {
	var out []event.RequirementProgress
	for _, r := range g.Requirements {
		out = append(out, event.RequirementProgress{ID: r.ID, LabelHash: fp.Hash(r.Label), Source: r.Source, State: r.Status, Evidence: append([]int64(nil), r.Evidence...)})
	}
	return out
}
func (g Graph) Text() string {
	var b strings.Builder
	for _, r := range g.Requirements {
		fmt.Fprintf(&b, "%s: %s · %s\n", r.ID, r.Label, r.Status)
	}
	return b.String()
}

func claimProse(text string) string {
	if len(text) > 65536 {
		text = text[:65536]
	}
	fence := false
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") || strings.HasPrefix(strings.TrimSpace(line), "~~~") {
			fence = !fence
			continue
		}
		if !fence {
			lines = append(lines, clipped(line, 256))
		}
		if len(lines) >= 8 {
			break
		}
	}
	return strings.Join(lines, " ")
}
