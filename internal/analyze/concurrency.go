package analyze

import (
	"fmt"
	"sort"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// heavySpan is a heavy test run with the time it occupied.
type heavySpan struct {
	r          *Result
	ev         *event.Event
	start, end time.Time
}

// heavy reports a test run of the whole suite, or one that took at least
// expensive. It mirrors the live judgment without the engine's memory of
// earlier runs, so a selected test is heavy here only by its own time.
func heavy(ev *event.Event, expensive time.Duration) bool {
	if ev.Tool != event.ToolShell || ev.Category != event.CatVerify || ev.ExitCode == nil || *ev.ExitCode == -1 || ev.Background {
		return false
	}
	if expensive > 0 && time.Duration(ev.DurationMS)*time.Millisecond >= expensive {
		return true
	}
	cmd := ev.Cmd
	if cmd == "" {
		cmd = ev.CmdNorm
	}
	for _, t := range classify.TestCommands(cmd) {
		if classify.CommandScope(t) == classify.ScopeFull {
			return true
		}
	}
	return false
}

// span is the time a run occupied. A completion of a background run is
// stamped at its end; any other call at its start.
func span(ev *event.Event) (time.Time, time.Time) {
	d := time.Duration(ev.DurationMS) * time.Millisecond
	if ev.OriginSeq > 0 {
		return ev.TS.Add(-d), ev.TS
	}
	return ev.TS, ev.TS.Add(d)
}

// Overlaps adds an s1.concurrent_heavy_check verdict to every heavy test
// run that started while a heavy run of another session was running in the
// same project folder. A session audited alone cannot see this, so it is a
// pass over the sessions audited together. Runs with no recorded duration
// occupy no time and are not judged.
func Overlaps(results []*Result, expensive time.Duration) int {
	byProject := map[string][]heavySpan{}
	for _, r := range results {
		if r == nil || r.Session == nil || r.Session.ProjectPath == "" {
			continue
		}
		for _, ev := range r.Session.Events {
			if ev.DurationMS <= 0 || ev.TS.IsZero() || !heavy(ev, expensive) {
				continue
			}
			s, e := span(ev)
			byProject[r.Session.ProjectPath] = append(byProject[r.Session.ProjectPath], heavySpan{r: r, ev: ev, start: s, end: e})
		}
	}
	projects := make([]string, 0, len(byProject))
	for p := range byProject {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	added := 0
	for _, p := range projects {
		spans := byProject[p]
		sort.SliceStable(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
		for j := range spans {
			later := spans[j]
			for i := 0; i < j; i++ {
				earlier := spans[i]
				if earlier.r == later.r || !later.start.Before(earlier.end) || later.start.Before(earlier.start) {
					continue
				}
				v := detect.Signal{
					ID: fmt.Sprintf("%s-overlap-%d", later.r.Session.ID, later.ev.Seq), Detector: "S1", Rule: "s1.concurrent_heavy_check",
					Confidence: 0.85, Level: detect.L1, Seq: later.ev.Seq, Evidence: []int64{later.ev.Seq}, Primary: true,
					Facts: map[string]any{"cmd": later.ev.CmdNorm, "other_cmd": earlier.ev.CmdNorm, "other": shortSession(earlier.r.Session.ID),
						"since": int(later.start.Sub(earlier.start).Minutes()), "target": earlier.ev.CmdNorm},
				}
				later.r.Verdicts = append(later.r.Verdicts, v)
				if later.r.Totals.Interventions == nil {
					later.r.Totals.Interventions = map[string]int{}
				}
				later.r.Totals.Interventions[v.Level.String()]++
				added++
				break // one verdict per run
			}
		}
	}
	return added
}

func shortSession(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
