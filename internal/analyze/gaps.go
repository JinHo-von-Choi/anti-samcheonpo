package analyze

import (
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// Gap patterns: spans the current rules do not count as waste but that look
// like the waste users report. They are candidates for human labels, not
// verdicts.
const (
	GapVerifyStreak  = "verify_streak_no_progress"
	GapDocsVerify    = "verify_after_docs_only"
	GapReviewRepeat  = "review_same_state"
	gapMinVerifyRuns = 3
)

// Gap is one candidate span inside a session.
type Gap struct {
	Session  string `json:"session"`
	Agent    string `json:"agent"`
	Pattern  string `json:"pattern"`
	StartSeq int64  `json:"start_seq"`
	EndSeq   int64  `json:"end_seq"`
	Events   int    `json:"events"`
	Tokens   int64  `json:"tokens"`
	Micro    int64  `json:"micro_krw"`
	Prompt   string `json:"prompt,omitempty"`
}

// Gaps finds candidate spans in an analyzed session. Spans already counted as
// waste are skipped so the output measures what the rules miss.
func Gaps(r *Result) []Gap {
	s := r.Session
	var out []Gap
	mk := func(pattern string, evs []*event.Event) Gap {
		g := Gap{Session: s.ID, Agent: s.Agent, Pattern: pattern, StartSeq: evs[0].Seq, EndSeq: evs[len(evs)-1].Seq, Prompt: shortPrompt(s.FirstPrompt)}
		for _, ev := range s.Events {
			if ev.Seq < g.StartSeq || ev.Seq > g.EndSeq {
				continue
			}
			g.Events++
			g.Tokens += ev.Usage.Total()
			g.Micro += ev.CostMicroKRW
		}
		return g
	}
	// verification runs with no progress and no waste verdict between them
	var streak []*event.Event
	flush := func() {
		if len(streak) >= gapMinVerifyRuns {
			out = append(out, mk(GapVerifyStreak, streak))
		}
		streak = nil
	}
	// a verification right after a change that touched documentation only
	var docsOnly *event.Event
	// reviews and subagents over the same workspace state
	reviews := map[string][]*event.Event{}
	for _, ev := range s.Events {
		if ev.Kind != event.KindTool {
			continue
		}
		switch {
		case ev.Bucket == BucketProgress || ev.Bucket == BucketWaste:
			if ev.Category == event.CatVerify || ev.Category == event.CatProduce {
				flush()
			}
		case ev.Category == event.CatVerify && ev.ExitCode != nil:
			streak = append(streak, ev)
		}
		if ev.Category == event.CatProduce && ev.Bucket != BucketWaste {
			if allDocs(ev.Paths) {
				docsOnly = ev
			} else {
				docsOnly = nil
			}
		}
		if ev.Category == event.CatVerify && ev.ExitCode != nil && docsOnly != nil && ev.Bucket != BucketWaste {
			out = append(out, mk(GapDocsVerify, []*event.Event{docsOnly, ev}))
			docsOnly = nil
		}
		if detect.IsReview(ev) && ev.WSBefore != "" && ev.Bucket != BucketWaste {
			reviews[ev.WSBefore] = append(reviews[ev.WSBefore], ev)
		}
	}
	flush()
	for _, evs := range reviews {
		if len(evs) >= 2 {
			out = append(out, mk(GapReviewRepeat, evs))
		}
	}
	return out
}

func allDocs(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if !detect.IsDocPath(p) {
			return false
		}
	}
	return true
}

func shortPrompt(p string) string {
	p = strings.Join(strings.Fields(p), " ")
	if r := []rune(p); len(r) > 80 {
		return string(r[:80]) + "…"
	}
	return p
}
