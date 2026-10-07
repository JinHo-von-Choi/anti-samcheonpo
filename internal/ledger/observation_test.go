package ledger

import (
	"path/filepath"
	"testing"
)

func TestObservationGapIsMonotonicAndIsolated(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, n := range []uint64{3, 1, 5} {
		if err := db.SaveObservationGap("claude", "s", n); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := db.ObservationGap("claude", "s"); err != nil || n != 5 {
		t.Fatalf("%d %v", n, err)
	}
	if n, err := db.ObservationGap("codex", "s"); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
	if err := db.SaveObservationGap("claude", "s", 0); err == nil {
		t.Fatal("zero cleared gap")
	}
}
