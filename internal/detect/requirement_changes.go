package detect

import (
	"regexp"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/alignment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

var networkCall = regexp.MustCompile(`(?i)\b(?:fetch|axios(?:\.(?:get|post|put|delete|request))?|requests?\.(?:get|post|put|delete|request)|http\.(?:Get|Post|Do)|client\.(?:get|post|send|request)|urllib\.[\w.]*urlopen)\s*\(`)
var mockCall = regexp.MustCompile(`(?i)\b(?:mock|stub|fake|Mock[A-Za-z_]+|Fake[A-Za-z_]+|fixture)[A-Za-z_0-9.]*\s*\(`)
var constantReturn = regexp.MustCompile("(?i)^\\s*return\\s+(?:\\{|true\\b|false\\b|None\\b|null\\b|[\"'`]|[0-9])")
var fixturePath = regexp.MustCompile(`(?i)(?:^|/)(?:mocks?|fixtures?|examples?)(?:/|\.)|(?:^|/)(?:mock_|fake_|stub_)`)

func countPattern(re *regexp.Regexp, lines []string) int {
	n := 0
	for _, l := range lines {
		n += len(re.FindAllStringIndex(l, -1))
	}
	return n
}
func boundedPatch(lines []string) []string {
	if len(lines) > 2048 {
		return nil
	}
	for _, l := range lines {
		if len(l) > 8192 {
			return nil
		}
	}
	return lines
}

func (e *Engine) observeRequirementChanges(ev *event.Event, sigs *[]Signal) {
	if e.Restoring || ev.Kind != event.KindTool || ev.IsError || ev.Category != event.CatProduce {
		return
	}
	if !ev.ResultObserved {
		e.deferCandidate(ev, "s3.requirement_substitution", "missing_result")
		return
	}
	if ev.Reliability != nil && ev.Reliability.Phase == "input" {
		e.deferCandidate(ev, "s3.requirement_substitution", "missing_result")
		return
	}
	goal := e.FirstPrompt
	if goal == "" && e.Accepted && e.Contract != nil {
		goal = e.Contract.Goal
	}
	constraints := alignment.Constraints(goal)
	if len(constraints) == 0 {
		return
	}
	for i, p := range ev.Patch {
		if i >= 32 {
			e.deferCandidate(ev, "s3.requirement_substitution", "candidate_limit")
			break
		}
		a, r := boundedPatch(p.Added), boundedPatch(p.Removed)
		if a == nil && len(p.Added) > 0 || r == nil && len(p.Removed) > 0 {
			e.deferCandidate(ev, "s3.requirement_substitution", "patch_incomplete")
			continue
		}
		for _, c := range constraints {
			kind := ""
			switch c.Kind {
			case "real_integration":
				if classify.IsTestPath(p.Path) || fixturePath.MatchString(strings.ReplaceAll(p.Path, "\\", "/")) {
					continue
				}
				if countPattern(networkCall, codeOnly(r)) > 0 && countPattern(networkCall, codeOnly(a)) == 0 && (countPattern(mockCall, codeOnly(a)) > 0 || countPattern(constantReturn, a) > 0) {
					kind = "real_to_mock"
				}
			case "preserve_checks":
				if !classify.IsTestPath(p.Path) {
					continue
				}
				for _, change := range TestChange(a, r) {
					if change != KindLiteralReplaced {
						kind = "check_weakened"
					}
				}
				if p.Deleted {
					kind = "check_deleted"
				}
			case "preserve_feature":
				re := regexp.MustCompile(`\b(?:def|function|func)\s+` + regexp.QuoteMeta(c.Name) + `\s*\(`)
				ref := regexp.MustCompile(`\b` + regexp.QuoteMeta(c.Name) + `\b`)
				if !classify.IsTestPath(p.Path) && countPattern(re, codeOnly(r)) > 0 && countPattern(ref, codeOnly(a)) == 0 {
					kind = "requested_feature_removed"
				}
			}
			if kind == "" {
				continue
			}
			path := fp.Hash(p.Path)
			ev.RequirementChanges = append(ev.RequirementChanges, event.RequirementChange{RequirementID: c.ID, Kind: kind, PathHash: path})
			e.add(sigs, Signal{Detector: "S3", Rule: "s3.requirement_substitution", Level: L1, Confidence: .65, Estimate: true, Evidence: []int64{ev.Seq}, Facts: map[string]any{"requirements": []string{c.ID}, "results": []int64{ev.Seq}, "kind": kind, "path_hash": path, "candidate_key": fp.Hash(c.ID, path, kind), "source": "request_bound_patch"}})
		}
	}
}
