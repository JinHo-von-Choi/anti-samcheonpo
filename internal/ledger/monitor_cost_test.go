package ledger

import (
	"path/filepath"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/judge"
)

func TestMonitoringCostMissingPendingSettledAndGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	if _, err := MonitoringCost(path); err == nil {
		t.Fatal("missing ledger treated as zero")
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if amount, err := MonitoringCost(path); err != nil || amount != 0 {
		t.Fatalf("empty measured ledger: %d %v", amount, err)
	}
	b := judge.Budget{}
	if err := b.Reserve("one", 100, 1000, 3); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveJudgeBudget("claude", "s", b); err != nil {
		t.Fatal(err)
	}
	if _, err := MonitoringCost(path); err == nil {
		t.Fatal("pending reservation treated as paid zero")
	}
	b.Settle(71, true)
	if err := db.SaveJudgeBudget("claude", "s", b); err != nil {
		t.Fatal(err)
	}
	if amount, err := MonitoringCost(path); err != nil || amount != 71 {
		t.Fatalf("measured %d %v", amount, err)
	}
	if _, err := db.Exec(`INSERT INTO observation_gap VALUES('claude','s',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := MonitoringCost(path); err == nil {
		t.Fatal("gap ignored")
	}
}
