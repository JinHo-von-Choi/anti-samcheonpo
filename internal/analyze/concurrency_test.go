package analyze

import (
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func heavyRunEv(seq int64, cmd string, at time.Time, d time.Duration) *event.Event {
	exit := 0
	return &event.Event{Seq: seq, Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify, Cmd: cmd, CmdNorm: cmd,
		ExitCode: &exit, TS: at, DurationMS: d.Milliseconds()}
}

func overlapResult(id, project string, evs ...*event.Event) *Result {
	return &Result{Session: &event.Session{ID: id, ProjectPath: project, Events: evs}}
}

func TestOverlapsAcrossSessions(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 13, 22, 0, 0, time.UTC)
	a := overlapResult("sess-a", "/w", heavyRunEv(1, "npx vitest run", t0, 4*time.Minute))
	b := overlapResult("sess-b", "/w", heavyRunEv(2, "npx playwright test", t0.Add(time.Minute), 3*time.Minute))
	// selected tests are not heavy unless they took long
	c := overlapResult("sess-c", "/w", heavyRunEv(3, "npx vitest run src/a.test.ts", t0.Add(time.Minute), 30*time.Second))
	// another folder is another machine budget
	d := overlapResult("sess-d", "/other", heavyRunEv(4, "npx vitest run", t0.Add(time.Minute), time.Minute))
	// after the first run ended
	e := overlapResult("sess-e", "/w", heavyRunEv(5, "npx vitest run", t0.Add(10*time.Minute), time.Minute))
	if n := Overlaps([]*Result{a, b, c, d, e}, 5*time.Minute); n != 1 {
		t.Fatalf("overlaps=%d", n)
	}
	if len(b.Verdicts) != 1 || b.Verdicts[0].Rule != "s1.concurrent_heavy_check" || b.Verdicts[0].Facts["other"] != "sess-a" || b.Totals.Interventions["L1"] != 1 {
		t.Fatalf("b=%+v", b.Verdicts)
	}
	for _, r := range []*Result{a, c, d, e} {
		if len(r.Verdicts) != 0 {
			t.Fatalf("%s: %+v", r.Session.ID, r.Verdicts)
		}
	}
}

func TestOverlapsUsesTheEndOfABackgroundCompletion(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 13, 0, 0, 0, time.UTC)
	done := heavyRunEv(9, "cargo test --workspace", t0.Add(10*time.Minute), 10*time.Minute)
	done.OriginSeq = 3 // stamped at its end: it ran from t0
	a := overlapResult("sess-a", "/w", done)
	b := overlapResult("sess-b", "/w", heavyRunEv(1, "cargo test --workspace", t0.Add(2*time.Minute), time.Minute))
	if n := Overlaps([]*Result{a, b}, 5*time.Minute); n != 1 || len(b.Verdicts) != 1 {
		t.Fatalf("n=%d b=%+v", n, b.Verdicts)
	}
}
