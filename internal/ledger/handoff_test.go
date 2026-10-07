package ledger

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"path/filepath"
	"testing"
)

func TestHandoffUsageDistinguishesMissingPricingAndAgentCollision(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.UpsertLive("s", "claude", "root", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertLive("s", "codex", "other", "", ""); err == nil {
		t.Fatal("another agent overwrote live identity")
	}
	u, err := db.HandoffUsage("claude", "s")
	if err != nil || u.Tokens != nil || u.APIEquivalentMicro != nil {
		t.Fatalf("%+v %v", u, err)
	}
	ev := &event.Event{Seq: 1, Kind: event.KindMessage, Usage: event.Usage{In: 10}, Priced: true, CostMicroKRW: 100}
	if err := db.InsertLiveEvent("s", ev); err != nil {
		t.Fatal(err)
	}
	u, err = db.HandoffUsage("claude", "s")
	if err != nil || u.Tokens == nil || *u.Tokens != 10 || u.APIEquivalentMicro == nil || *u.APIEquivalentMicro != 100 {
		t.Fatalf("%+v %v", u, err)
	}
	u, err = db.HandoffUsage("codex", "s")
	if err != nil || u.Tokens != nil {
		t.Fatalf("collision: %+v %v", u, err)
	}
	ev.Priced = false
	if err := db.InsertLiveEvent("s", ev); err != nil {
		t.Fatal(err)
	}
	u, err = db.HandoffUsage("claude", "s")
	if err != nil || u.Tokens == nil || u.APIEquivalentMicro != nil {
		t.Fatalf("unknown price: %+v %v", u, err)
	}
}

func TestFailedApproachesExcludeDeniedAndSuccessfulCommands(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.UpsertLive("s", "claude", "root", "", ""); err != nil {
		t.Fatal(err)
	}
	for i, code := range []int{-1, 0, 1, 2} {
		x := code
		ev := &event.Event{Seq: int64(i), Kind: event.KindTool, Tool: event.ToolShell, ExitCode: &x, CmdFP: "cmd", ResultFP: "result", Summary: "observed execution"}
		if err := db.InsertLiveEvent("s", ev); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.FailedApproaches("claude", "s")
	if err != nil || len(got) != 2 || got[0].ExitCode != 2 || got[1].ExitCode != 1 || got[0].CommandHash != "cmd" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = db.FailedApproaches("codex", "s")
	if err != nil || len(got) != 0 {
		t.Fatalf("wrong agent: %+v %v", got, err)
	}
}
