package bench

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
)

func TestScheduleSeedAndBlocks(t *testing.T) {
	cfg := ABConfig{Seed: 71, Trials: 5, Arms: map[string]Arm{"on": {}, "off": {}, "simple": {}}}
	tasks := []LiveTask{{Name: "a"}, {Name: "b"}}
	a, err := Schedule(tasks, cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Schedule(tasks, cfg, "")
	if !reflect.DeepEqual(a, b) {
		t.Fatal("seed did not reproduce order")
	}
	if len(a) != 30 {
		t.Fatal(len(a))
	}
	for i := 0; i < len(a); i += 3 {
		arms := map[string]bool{}
		for _, r := range a[i : i+3] {
			arms[r.Arm] = true
			if r.Task.Name != a[i].Task.Name || r.Trial != a[i].Trial {
				t.Fatal("split block")
			}
		}
		if len(arms) != 3 {
			t.Fatal("missing arm")
		}
	}
	cfg.Seed++
	b, _ = Schedule(tasks, cfg, "")
	if reflect.DeepEqual(a, b) {
		t.Fatal("seed did not change order")
	}
	if _, err := Schedule(tasks, cfg, "missing"); err == nil {
		t.Fatal("unknown arm accepted")
	}
}

func TestValidateRowsRejectsDuplicationAndMixedProtocols(t *testing.T) {
	a := TrialResult{ProtocolVersion: "2", RunID: "run", Arm: "off", Task: "a", Trial: 1, Agent: "claude", SessionID: "one", CostKnown: true, TotalKRW: 10}
	if err := ValidateRows([]TrialResult{a}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"duplicate", "session", "protocol", "negative", "waste", "future"} {
		t.Run(kind, func(t *testing.T) {
			b := a
			b.Trial++
			switch kind {
			case "duplicate":
				b = a
			case "session":
			case "protocol":
				b.ProtocolVersion = ""
			case "negative":
				b.TotalKRW = -1
			case "waste":
				b.WasteKRW = 11
			case "future":
				b.ProtocolVersion = "3"
			}
			if err := ValidateRows([]TrialResult{a, b}); err == nil {
				t.Fatal("invalid rows accepted")
			}
		})
	}
}

func TestSummaryMissingZeroAndBoundary(t *testing.T) {
	rows := []TrialResult{{Arm: "off", Verified: true, CostKnown: true, TotalKRW: 100}, {Arm: "off", CostKnown: true, TotalKRW: 100}, {Arm: "zero", Verified: true, CostKnown: true}, {Arm: "unknown", Verified: true}, {Arm: "failed", CostKnown: true, TotalKRW: 100}}
	for _, s := range Summarize(rows, "off") {
		switch s.Arm {
		case "off":
			if s.CostPerVerified != 200 || !s.CostPerVerifiedKnown {
				t.Fatal(s)
			}
		case "zero":
			if !s.CostPerVerifiedKnown || s.CostPerVerified != 0 {
				t.Fatal(s)
			}
		case "unknown":
			if s.CostPerVerifiedKnown || s.CostMissingRate != 1 {
				t.Fatal(s)
			}
		case "failed":
			if s.CostPerVerifiedKnown {
				t.Fatal(s)
			}
		}
		if s.NonInferior != nil || s.MeetsCostGoal != nil {
			t.Fatal("pilot claimed efficacy")
		}
	}
	rows = nil
	for i := 0; i < 30; i++ {
		rows = append(rows, TrialResult{Arm: "off", Verified: true}, TrialResult{Arm: "on", Verified: true})
	}
	s := Summarize(rows, "off")[1]
	if s.DiffLo >= 0 || s.NonInferior != nil {
		t.Fatalf("100%% success is not zero uncertainty: %+v", s)
	}
}

func TestTranscriptIdentityFreshnessAndAmbiguity(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../testdata/claude/basic.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Second)
	if _, err := ResolveTranscript("claude", "s1", "/w/proj", root, "", start); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveTranscript("claude", "s1", "/wrong", root, "", start); err == nil {
		t.Fatal("wrong workspace")
	}
	if _, err := ResolveTranscript("claude", "s1", "/w/proj", root, "", time.Now().Add(time.Hour)); err == nil {
		t.Fatal("stale source")
	}
	if _, err := ResolveTranscript("claude", "s2", "/w/proj", root, "", start); err == nil {
		t.Fatal("wrong session")
	}
	other := filepath.Join(root, "other")
	os.Mkdir(other, 0o755)
	os.WriteFile(filepath.Join(other, "s1.jsonl"), b, 0o600)
	if _, err := ResolveTranscript("claude", "s1", "/w/proj", root, "", start); err == nil {
		t.Fatal("ambiguous sources")
	}
	if sessionID([]byte("{\"type\":\"thread.started\",\"thread_id\":\"cx\"}\n")) != "cx" {
		t.Fatal("codex output")
	}
	if sessionID([]byte("{\"session_id\":\"a\"}\n{\"session_id\":\"b\"}")) != "" {
		t.Fatal("mixed output")
	}
}

func TestExternalGraderRejectsTestWeakening(t *testing.T) {
	tasks, err := LoadLiveTasks("../../bench/live")
	if err != nil {
		t.Fatal(err)
	}
	var task LiveTask
	for _, candidate := range tasks {
		if candidate.Name == "fix-median" {
			task = candidate
		}
	}
	if task.Name == "" {
		t.Fatal("median task missing")
	}
	prices, _ := cost.Load(filepath.Join(t.TempDir(), "none"))
	for _, tc := range []struct {
		name, script string
		pass         bool
	}{
		{"weaken", "printf '' > test_stats.py", false},
		{"exit-without-checks", "printf 'raise SystemExit(0)\n' > stats.py", false},
		{"fix", "printf 'def median(xs):\n s=sorted(xs); n=len(s); return (s[(n-1)//2]+s[n//2])/2\n' > stats.py", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := RunTrial(task, "test", Arm{Cmd: []string{"sh", "-c", tc.script}}, 0, ABConfig{TimeoutSec: 10}, prices, t.TempDir(), t.TempDir())
			if r.Error != "" || r.Verified != tc.pass || !r.IndependentGrader || r.GraderDigest == "" {
				t.Fatalf("%+v", r)
			}
			if r.CostKnown || r.CostError == "" {
				t.Fatal("missing usage became zero cost")
			}
		})
	}
}

func TestTreeDigestContentAndSymlink(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a")
	os.WriteFile(p, []byte("one"), 0o600)
	a, err := TreeDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p, []byte("two"), 0o600)
	b, err := TreeDigest(dir)
	if err != nil || a == b {
		t.Fatal("missed content change")
	}
	if err := os.Symlink(p, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := TreeDigest(dir); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal("symlink accepted")
	}
}
