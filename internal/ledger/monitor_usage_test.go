package ledger

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/judge"
	"path/filepath"
	"testing"
)

func TestMonitoringTokensRequireEverySettledCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if usage, err := MonitoringUsage(path); err != nil || usage.Total() != 0 {
		t.Fatal(usage, err)
	}
	b := judge.Budget{Calls: 1, Seen: map[string]bool{"call": true}}
	if err = db.SaveJudgeBudget("claude", "s", b); err != nil {
		t.Fatal(err)
	}
	if _, err := MonitoringUsage(path); err == nil {
		t.Fatal("settled cost without a token event reported as zero")
	}
	if _, err = db.Exec(`INSERT INTO session(id,agent,mode) VALUES('s','claude','audit')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO event(session_id,seq,kind,tool,tokens_in,tokens_out,tokens_cache_read,tokens_cache_write) VALUES('s',1,'tool','judge',10,2,3,4)`); err != nil {
		t.Fatal(err)
	}
	if usage, err := MonitoringUsage(path); err != nil || usage.Total() != 19 {
		t.Fatal(usage, err)
	}
	b.Unknown = true
	b.ReservedMicro = 10
	if err = db.SaveJudgeBudget("claude", "s", b); err != nil {
		t.Fatal(err)
	}
	if _, err := MonitoringCost(path); err == nil {
		t.Fatal("unknown monetary charge presented as settled")
	}
	if usage, err := MonitoringUsage(path); err != nil || usage.Total() != 19 {
		t.Fatal("known tokens lost because monetary amount is unknown", usage, err)
	}
}
