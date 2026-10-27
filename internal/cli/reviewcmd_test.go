package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

// Each answer lands on the intervention and in feedback, and accuracy
// reports the judged precision from those answers.
func TestReviewRecordsJudgmentsAndAccuracyCountsThem(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	d, err := ledger.Open(filepath.Join(home, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertLive("s1", "claude", t.TempDir(), "", ""); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, id := range []string{"v1", "v2", "v3"} {
		at := now.Add(time.Duration(-i) * time.Minute).Format(time.RFC3339Nano)
		if _, err := d.Exec(`INSERT INTO intervention(verdict_id,session_id,seq,channel,level,blocked,rule,delivery_state,emitted_at) VALUES(?,'s1',?,'agent',1,0,'s4.read_only_streak','delivered',?)`, id, i, at); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()

	c := reviewCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetIn(strings.NewReader("y\nn\n\n"))
	c.SetArgs(nil)
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "맞음 1, 오탐 1, 모름 0") {
		t.Fatal(out.String())
	}

	d, err = ledger.Open(filepath.Join(home, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	choices := map[string]string{}
	rows, err := d.Query(`SELECT verdict_id, COALESCE(user_choice,'') FROM intervention`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, choice string
		if err := rows.Scan(&id, &choice); err != nil {
			t.Fatal(err)
		}
		choices[id] = choice
	}
	rows.Close()
	if choices["v1"] != ledger.LabelTruePositive || choices["v2"] != ledger.LabelFalsePositive || choices["v3"] != "" {
		t.Fatalf("choices %v", choices)
	}
	left, err := d.UnreviewedInterventions(time.Time{}, 10)
	d.Close()
	if err != nil || len(left) != 1 || left[0].Verdict.ID != "v3" {
		t.Fatalf("unreviewed %+v %v", left, err)
	}

	a := accuracyCmd()
	var acc bytes.Buffer
	a.SetOut(&acc)
	a.SetArgs([]string{"--since", "all"})
	if err := a.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(acc.String(), "50.0") {
		t.Fatalf("judged precision missing:\n%s", acc.String())
	}
}

func TestReviewTakesRulesInTurn(t *testing.T) {
	row := func(rule string) ledger.ReviewRow {
		var r ledger.ReviewRow
		r.Verdict.Rule = rule
		return r
	}
	rows := []ledger.ReviewRow{row("s9.a"), row("s9.a"), row("s9.a"), row("s3.b"), row("s5.c"), row("s3.b")}
	var got []string
	for _, r := range spreadRules(rows, 5) {
		got = append(got, r.Verdict.Rule)
	}
	if strings.Join(got, " ") != "s9.a s3.b s5.c s9.a s3.b" {
		t.Fatalf("%v", got)
	}
	if n := len(spreadRules(rows, 10)); n != len(rows) {
		t.Fatalf("a limit above the pool keeps every row: %d", n)
	}
}
