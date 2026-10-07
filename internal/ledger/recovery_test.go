package ledger

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
)

func TestRecoveryTransitionsPersistAndRejectIdentityReplacement(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	now := time.Now()
	a := recovery.Attempt{Version: "recovery/1", ID: "a", Agent: "claude", SessionID: "s", Revision: 1, CauseKey: "key", VerdictID: "v", CreatedAt: now, Stage: recovery.Proposed, Prescription: recovery.For(recovery.Environment), Route: "advice"}
	if err := d.SaveRecovery(a); err != nil {
		t.Fatal(err)
	}
	bad := a
	bad.SessionID = "other"
	if err := d.SaveRecovery(bad); err == nil {
		t.Fatal("identity overwritten")
	}
	if err := a.Advance(recovery.Emitted, now); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveRecovery(a); err != nil {
		t.Fatal(err)
	}
	if err := a.Advance(recovery.Delivered, now); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveRecovery(a); err != nil {
		t.Fatal(err)
	}
	bad = a
	bad.Stage = recovery.Proposed
	if err := d.SaveRecovery(bad); err == nil {
		t.Fatal("state regressed")
	}
	rows, err := d.Recoveries("claude", "s")
	if err != nil || len(rows) != 1 || rows[0].Stage != recovery.Delivered {
		t.Fatalf("%+v %v", rows, err)
	}
}
