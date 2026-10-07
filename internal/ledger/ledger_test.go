package ledger

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/claude"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
)

func result(t *testing.T) *analyze.Result {
	s, err := claude.ParseFile(filepath.Join("..", "..", "testdata", "claude", "basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := cost.Load("")
	cfg := config.Default()
	r, err := analyze.Run(s, analyze.Options{Config: cfg, ConfigHash: config.Hash(cfg), Prices: p})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMigrationsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.db")
	d, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	v := d.Version()
	d.Close()
	d2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	if v < 2 || d2.Version() != v {
		t.Fatalf("version %d then %d", v, d2.Version())
	}
}

func TestSaveAndAggregate(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	r := result(t)
	sl, err := d.SaveAnalysis(r, 1, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	// saving again replaces rows instead of duplicating them
	if _, err := d.SaveAnalysis(r, 1, 2, nil); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM event WHERE session_id=?`, r.Session.ID).Scan(&n)
	if n != len(r.Session.Events) {
		t.Fatalf("event rows %d, want %d", n, len(r.Session.Events))
	}
	bt := d.BucketTotals([]string{r.Session.ID})[r.Session.ID]
	if bt.Micro != r.Totals.Micro || bt.Tokens != r.Totals.Tokens {
		t.Errorf("stored totals %d/%d differ from analysis %d/%d", bt.Micro, bt.Tokens, r.Totals.Micro, r.Totals.Tokens)
	}
	for k, v := range r.Totals.BucketMicro {
		if bt.BucketMicro[k] != v {
			t.Errorf("bucket %s %d != %d", k, bt.BucketMicro[k], v)
		}
	}
	got, err := d.Seal(r.Session.ID)
	if err != nil || got.Head != sl.Head || got.Rows != len(r.Session.Events)+len(r.Verdicts) {
		t.Fatalf("seal %+v %v", got, err)
	}
	if d.NeedsAudit(r.Session.SourcePath, 1, 2) {
		t.Error("an unchanged source does not need a new audit")
	}
	if !d.NeedsAudit(r.Session.SourcePath, 1, 3) {
		t.Error("a changed source needs a new audit")
	}
	rows, err := d.Sessions(time.Time{}, "all", "")
	if err != nil || len(rows) != 1 || rows[0].Micro != r.Totals.Micro {
		t.Fatalf("sessions %+v %v", rows, err)
	}
}

func TestFeedback(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for i := 0; i < 3; i++ {
		if err := d.AddFeedback("v", "s", "s1.identical_rerun", "false_positive", "normal", "", "/w/p"); err != nil {
			t.Fatal(err)
		}
	}
	_ = d.AddFeedback("v", "s", "s1.identical_rerun", "ignore_once", "now", "", "/w/p")
	if n := d.FalsePositives("/w/p", "s1.identical_rerun", time.Time{}); n != 3 {
		t.Errorf("false positives %d", n)
	}
	if d.LastFalsePositive("/w/p", "s1.identical_rerun").IsZero() {
		t.Error("last false positive time")
	}
}
