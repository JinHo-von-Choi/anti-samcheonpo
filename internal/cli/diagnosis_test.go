package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWhyReadsLedgerWithoutMutationAndLabelsLedgerOnlyInterval(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	path, err := filepath.Abs("../../testdata/claude/basic.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	prices, _ := cost.Load("")
	cfg := config.Default()
	r, err := analyzeFile(sources.File{Path: path, Agent: "claude"}, cfg, config.Hash(cfg), prices)
	if err != nil {
		t.Fatal(err)
	}
	r.Session.SourcePath = ""
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SaveAnalysis(r, 1, 2, nil); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(dbPath())
	if err != nil {
		t.Fatal(err)
	}
	out := new(bytes.Buffer)
	cmd := diagnosticCmd("why")
	cmd.SetOut(out)
	cmd.SetArgs([]string{r.Session.ID, "--format", "json"})
	if err = cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err = json.Unmarshal(out.Bytes(), &report); err != nil || report["completion"] != "unknown" {
		t.Fatalf("%s %v", out, err)
	}
	after, _ := os.ReadFile(dbPath())
	if !bytes.Equal(before, after) {
		t.Fatal("read-only diagnosis changed ledger")
	}
	labelDir := t.TempDir()
	label := labelCmd()
	label.SetOut(new(bytes.Buffer))
	a, b := r.Session.Events[0].Seq, r.Session.Events[len(r.Session.Events)-1].Seq
	label.SetArgs([]string{r.Session.ID, "--labeler", "human", "--dir", labelDir, "--start", fmt.Sprint(a), "--end", fmt.Sprint(b), "--symptom", "S2", "--outcome", "unknown"})
	if err = label.Execute(); err != nil {
		t.Fatal(err)
	}
	labels, err := eval.LoadLabels(labelDir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range labels {
		if l.Meta == nil && l.Symptom == "S2" && l.Outcome == "unknown" {
			found = true
		}
	}
	if !found {
		t.Fatal(labels)
	}
	for _, tc := range []struct {
		name, input, page string
		want              bool
	}{
		{"quit-without-confirmation", "q\n", "1000", false},
		{"unseen-pages", "reviewed yes\nq\n", "1", false},
		{"explicit-complete-review", "reviewed yes\nq\n", "1000", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			c := labelCmd()
			c.SetOut(new(bytes.Buffer))
			c.SetIn(strings.NewReader(tc.input))
			c.SetArgs([]string{r.Session.ID, "--labeler", "human", "--dir", dir, "--page", tc.page})
			if err := c.Execute(); err != nil {
				t.Fatal(err)
			}
			ls, err := eval.LoadLabels(dir)
			if err != nil || len(ls) != 1 || ls[0].Meta == nil || ls[0].Meta.Reviewed != tc.want {
				t.Fatalf("%+v %v", ls, err)
			}
		})
	}
	read, err := ledger.OpenReadOnly(dbPath())
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	evs, err := read.EventsOf(r.Session.ID)
	if err != nil || len(evs) != len(r.Session.Events) {
		t.Fatal("label corrupted ledger")
	}
}
func TestBenchCheckWithInsufficientEvidenceFails(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rows.jsonl")
	if err := os.WriteFile(p, []byte("{\"arm\":\"off\",\"task\":\"a\"}\n{\"arm\":\"on\",\"task\":\"a\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := benchReportCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{p, "--check", "--format", "json"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "근거 부족") {
		t.Fatalf("%v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != 2 || rows[1]["success_margin"] != .02 {
		t.Fatalf("%s %v", out, err)
	}
}
