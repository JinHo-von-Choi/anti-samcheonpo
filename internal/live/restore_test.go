package live

import (
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// A daemon restarted under a running session (an upgrade, an idle exit)
// carries the session on: earlier events are not overwritten, new ones are
// numbered after them, and the waste found before the restart still counts.
func TestRestartedDaemonContinuesSession(t *testing.T) {
	s, _ := reliabilitySession(t)
	exit := 1
	for i := 0; i < 4; i++ {
		ev := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Cmd: "pytest -q", CmdNorm: "pytest -q", CmdFP: "cmd-pytest",
			Category: event.CatVerify, ExitCode: &exit, ResultFP: "r1", WSBefore: "ws", WSAfter: "ws", TS: time.Now(), CostMicroKRW: 40_000_000, Priced: true}
		s.mu.Lock()
		s.parser.AddEvent(ev)
		s.persistEvent(ev)
		for _, v := range s.eng.Observe(ev) {
			s.persistVerdict(v)
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	_, _, wasteBefore, _ := s.eng.St.UsageSummary()
	total := s.eng.St.TotalMicro
	s.mu.Unlock()
	if wasteBefore == 0 {
		t.Fatal("setup: identical failing reruns are waste")
	}
	stored, err := s.db.EventsOf(s.ID)
	if err != nil || len(stored) != 4 {
		t.Fatalf("setup: %d events stored: %v", len(stored), err)
	}
	prices, _ := cost.Load("")
	again, err := newSession(s.ID, "claude", s.Root, "", s.db, prices, adapter.Profiles["claude"].Caps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = again.Finalize() })
	again.mu.Lock()
	_, _, wasteAfter, _ := again.eng.St.UsageSummary()
	if again.eng.St.TotalMicro != total || wasteAfter != wasteBefore {
		t.Fatalf("totals carried over: spend %d/%d waste %d/%d", again.eng.St.TotalMicro, total, wasteAfter, wasteBefore)
	}
	next := &event.Event{Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{"a.go"}, TS: time.Now()}
	again.parser.AddEvent(next)
	again.persistEvent(next)
	again.mu.Unlock()
	if next.Seq != stored[len(stored)-1].Seq+1 {
		t.Fatalf("new events continue the numbering: got %d after %d", next.Seq, stored[len(stored)-1].Seq)
	}
	all, _ := s.db.EventsOf(s.ID)
	if len(all) != 5 || all[0].CmdFP != "cmd-pytest" {
		t.Fatalf("earlier events kept, one added: %d", len(all))
	}
}
