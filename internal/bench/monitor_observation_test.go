package bench

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

func TestMonitorAttachmentRequiresMatchingSessionEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	db, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.UpsertLive("observed", "codex", t.TempDir(), "", "model"); err != nil {
		t.Fatal(err)
	}
	if err = db.InsertLiveEvent("observed", &event.Event{Kind: event.KindTool, Tool: event.ToolShell, TS: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if n, _, problem := monitorObservation(path, "missing"); n != 0 || problem == "" {
		t.Fatal("unrelated session accepted")
	}
	if n, d, problem := monitorObservation(path, "observed"); n != 1 || d != 0 || problem != "" {
		t.Fatal(n, d, problem)
	}
}
