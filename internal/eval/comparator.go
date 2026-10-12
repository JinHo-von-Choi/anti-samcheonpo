package eval

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
)

// ReferencePrediction is a projection onto our normalized trace, not an
// invocation or reproduction of a competing SDK's complete runtime.
type ReferencePrediction struct {
	Detector       string         `json:"detector"`
	Implementation string         `json:"implementation"`
	Reference      string         `json:"reference"`
	Parameters     map[string]int `json:"parameters"`
	Evidence       [][]int64      `json:"evidence"`
	Unknown        int            `json:"unknown_events"`
	SourceDigest   string         `json:"source_digest"`
}

func ReferencePredictions(events []*event.Event, editThreshold int) []ReferencePrediction {
	repeat := ReferencePrediction{Detector: "identical_action_result", Implementation: "normalized_projection/1; SDK not executed", Reference: "https://github.com/OpenHands/software-agent-sdk/blob/c8a65f0db7c4e5b455a124989c3a6a0a79fcd9e9/openhands-sdk/openhands/sdk/conversation/stuck_detector.py", Parameters: map[string]int{"repeats": 4, "event_window": 20}, Evidence: [][]int64{}}
	edit := ReferencePrediction{Detector: "per_file_edit_count", Implementation: "authored_heuristic/1; article does not specify N", Reference: "https://www.langchain.com/blog/improving-deep-agents-with-harness-engineering", Parameters: map[string]int{"edits": editThreshold}, Evidence: [][]int64{}}
	repeat.SourceDigest = rulepack.Digest(events)
	edit.SourceDigest = repeat.SourceDigest
	type pair struct {
		seq            int64
		action, result string
		position       int
	}
	var pairs []pair
	counts := map[string][]int64{}
	for position, e := range events {
		if e == nil {
			repeat.Unknown++
			edit.Unknown++
			continue
		}
		if e.Kind == event.KindPrompt {
			pairs = nil
			counts = map[string][]int64{}
			continue
		}
		if e.Kind != event.KindTool {
			continue
		}
		if !e.ResultObserved || e.ActionFP == "" || e.ObservationFP == "" {
			repeat.Unknown++
			pairs = nil
		} else {
			pairs = append(pairs, pair{e.Seq, e.ActionFP, e.ObservationFP, position})
			if len(pairs) > 4 {
				pairs = pairs[len(pairs)-4:]
			}
			if len(pairs) == 4 && position-pairs[0].position < 20 {
				equal := true
				seqs := []int64{}
				for _, p := range pairs {
					equal = equal && p.action == pairs[0].action && p.result == pairs[0].result
					seqs = append(seqs, p.seq)
				}
				if equal {
					repeat.Evidence = append(repeat.Evidence, seqs)
				}
			}
		}
		if e.Tool != event.ToolEdit && e.Tool != event.ToolWrite {
			continue
		}
		if !e.ResultObserved || len(e.Paths) == 0 {
			edit.Unknown++
			continue
		}
		if e.IsError {
			continue
		}
		seen := map[string]bool{}
		for _, path := range e.Paths {
			if seen[path] {
				continue
			}
			seen[path] = true
			counts[path] = append(counts[path], e.Seq)
			if len(counts[path]) == editThreshold {
				edit.Evidence = append(edit.Evidence, append([]int64(nil), counts[path]...))
			}
			if len(counts[path]) > editThreshold {
				counts[path] = counts[path][1:]
			}
		}
	}
	return []ReferencePrediction{repeat, edit}
}

// ReviewConsensus requires matching votes from at least two IDs. A coordinator
// must independently establish that those IDs belong to distinct humans.
func ReviewConsensus(item ReviewItem, labels []Label) (string, string) {
	votes := map[string]string{}
	for _, l := range labels {
		if l.Meta != nil || l.Session != item.Session || l.Start != item.Start || l.End != item.End || l.Labeler == "" {
			continue
		}
		vote := "unknown"
		if l.Symptom == "none" && l.Justified == "no" {
			vote = "negative"
		}
		if len(l.Symptom) == 2 && l.Symptom[0] == 'S' && l.Symptom[1] >= '1' && l.Symptom[1] <= '8' && l.Justified == "yes" {
			vote = "positive"
		}
		if old, ok := votes[l.Labeler]; ok && old != vote {
			return "unknown", "contradictory_reviewer_votes"
		}
		votes[l.Labeler] = vote
	}
	if len(votes) < 2 {
		return "unknown", "two_independent_human_reviews_required"
	}
	consensus := ""
	for _, v := range votes {
		if v == "unknown" {
			return "unknown", "uncertain_review"
		}
		if consensus != "" && consensus != v {
			return "unknown", "reviewer_disagreement"
		}
		consensus = v
	}
	return consensus, "matching_ids_human_identity_unverified"
}

// FrozenReviewConsensus excludes legacy/manual votes that cannot establish
// which immutable source and queue the reviewer actually assessed.
func FrozenReviewConsensus(item ReviewItem, queueDigest string, labels []Label) (string, string) {
	var bound []Label
	unbound := false
	for _, l := range labels {
		if l.Meta != nil || l.Session != item.Session || l.Start != item.Start || l.End != item.End {
			continue
		}
		if item.SourceDigest == "" || queueDigest == "" || l.ReviewSourceDigest != item.SourceDigest || l.ReviewQueueDigest != queueDigest {
			unbound = true
			continue
		}
		bound = append(bound, l)
	}
	state, reason := ReviewConsensus(item, bound)
	if state == "unknown" && reason == "two_independent_human_reviews_required" && unbound {
		reason = "review_source_not_bound"
	}
	return state, reason
}
