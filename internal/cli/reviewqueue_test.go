package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
)

func TestPrepareReviewRequiresNativeSource(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	raw, err := os.ReadFile("../../conformance/cases/claude-behavior-alias-loop/input.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source.jsonl")
	if err = os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := sources.Parse(sources.File{Path: source, Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	s.Mode = "audit"
	prices, _ := cost.Load("")
	r, err := analyze.Run(s, analyze.Options{Config: config.Default(), Prices: prices})
	if err != nil {
		t.Fatal(err)
	}
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.SaveAnalysis(r, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	q, err := prepareQueue(db, time.Time{}, 1, 1, true)
	if err != nil || len(q.Items) != 1 || q.Items[0].NativeSourceSHA256 == "" || q.LabelsCreated {
		t.Fatalf("native queue: %+v %v", q, err)
	}
	if err = os.Remove(source); err != nil {
		t.Fatal(err)
	}
	q, err = prepareQueue(db, time.Time{}, 1, 1, true)
	if err != nil || len(q.Items) != 0 {
		t.Fatalf("missing source selected: %+v %v", q, err)
	}
	q, err = prepareQueue(db, time.Time{}, 1, 1)
	if err != nil || len(q.Items) != 1 || q.Items[0].NativeSourceAvailable {
		t.Fatalf("legacy observation queue changed: %+v %v", q, err)
	}
}
