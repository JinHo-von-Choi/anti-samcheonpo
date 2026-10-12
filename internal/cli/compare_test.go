package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
)

func TestCompareExcludesLaterTurnsAndDoesNotInventAccuracy(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	source, err := filepath.Abs("../../conformance/cases/claude-requirement-real-to-mock/input.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	s, err := sources.Parse(sources.File{Path: source, Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	prices, _ := cost.Load("")
	r, err := analyze.Run(s, analyze.Options{Config: config.Default(), Prices: prices})
	if err != nil {
		t.Fatal(err)
	}
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SaveAnalysis(r, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	events, err := db.EventsOf(s.ID)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	sha, err := eval.NativeSourceDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	first := events[0].Seq
	q := eval.ReviewQueue{Version: "review-queue/2", Controls: 1, Items: []eval.ReviewItem{{Session: s.ID, Agent: "claude", Start: first, End: first, Stratum: "unreviewed_control", SourceDigest: eval.ReviewSourceDigest(events), NativeSourceSHA256: sha}}}
	q.Digest = eval.ReviewQueueDigest(q)
	raw, _ := json.Marshal(q)
	queue := filepath.Join(t.TempDir(), "queue.json")
	if err = os.WriteFile(queue, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := compareCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"--queue", queue})
	if err = cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	labelDir := t.TempDir()
	label := labelCmd()
	label.SetOut(new(bytes.Buffer))
	label.SetArgs([]string{s.ID, "--labeler", "reviewer", "--dir", labelDir, "--queue", queue, "--start", fmt.Sprint(first), "--end", fmt.Sprint(first), "--symptom", "none", "--justified", "no"})
	if err := label.Execute(); err != nil {
		t.Fatal(err)
	}
	votes, err := eval.LoadLabels(labelDir)
	if err != nil || len(votes) != 2 || votes[1].ReviewSourceDigest != q.Items[0].SourceDigest || votes[1].ReviewQueueDigest != q.Digest {
		t.Fatalf("CLI review source not bound: %v %v", votes, err)
	}
	var report struct {
		Version string                `json:"version"`
		Scores  eval.ComparisonScores `json:"descriptive_scores"`
		Runs    []struct {
			References []eval.ReferencePrediction `json:"references"`
			Rules      map[string]int             `json:"current_replay_rules"`
		} `json:"runs"`
	}
	if err = json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Version != "reference-comparison/3" || report.Scores.CommonEvaluated != 0 || len(report.Runs) != 1 || len(report.Runs[0].Rules) != 0 {
		t.Fatal(report)
	}
	for _, p := range report.Runs[0].References {
		if len(p.Evidence) != 0 || p.Unknown != 0 {
			t.Fatal("later tool observations leaked into first prompt interval", p)
		}
	}
	for _, s := range report.Scores.Scores {
		if s.Precision != nil || s.Recall != nil {
			t.Fatal("unreviewed interval given accuracy", s)
		}
	}
}
