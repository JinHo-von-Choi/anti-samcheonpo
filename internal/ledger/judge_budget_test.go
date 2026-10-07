package ledger

import (
	"path/filepath"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/judge"
)

func TestJudgeReservationSurvivesRestartAndCannotReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var b judge.Budget
	if err := b.Reserve("input", 60, 100, 3); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveJudgeBudget("claude", "s", b); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.JudgeBudget("claude", "s")
	if err != nil || !got.Pending || got.ReservedMicro != 60 || got.Calls != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	got.Settle(0, false)
	if err := d.SaveJudgeBudget("claude", "s", got); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveJudgeBudget("claude", "s", judge.Budget{}); err == nil {
		t.Fatal("budget reset succeeded")
	}
	other, err := d.JudgeBudget("codex", "s")
	if err != nil || other.Calls != 0 {
		t.Fatal("agent identities merged")
	}
}
