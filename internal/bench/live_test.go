package bench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
)

func TestLiveTrialAndSummary(t *testing.T) {
	root := t.TempDir()
	task := filepath.Join(root, "tasks", "fix-add")
	_ = os.MkdirAll(filepath.Join(task, "repo"), 0o755)
	_ = os.WriteFile(filepath.Join(task, "repo", "add.txt"), []byte("broken\n"), 0o644)
	_ = os.WriteFile(filepath.Join(task, "task.yml"), []byte("prompt: fix add.txt\ncheck: grep -q fixed add.txt\n"), 0o644)
	tasks, err := LoadLiveTasks(filepath.Join(root, "tasks"))
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks %v %v", tasks, err)
	}
	claudeDir := filepath.Join(root, "claude")
	_ = os.MkdirAll(filepath.Join(claudeDir, "proj"), 0o755)
	tr, _ := filepath.Abs("../../testdata/claude/basic.jsonl")
	// a fake agent: fixes the file when asked, copies a real transcript and
	// prints its session id like claude -p --output-format json
	agent := filepath.Join(root, "agent.sh")
	_ = os.WriteFile(agent, []byte(`#!/bin/sh
[ "$FIX" = 1 ] && echo fixed > add.txt
sed -e "s|s1|sid-$FIX|g" -e "s|/w/proj|$PWD|g" "$TR" > "$CD/proj/sid-$FIX.jsonl"
test -n "$SAMCHEONPO_HOME" || exit 3
echo '{"type":"result","session_id":"sid-'$FIX'"}'
`), 0o755)
	cfgPath := filepath.Join(root, "ab.yml")
	_ = os.WriteFile(cfgPath, []byte(`trials: 1
arms:
  off: {cmd: ["`+agent+`", "{prompt}"], env: {FIX: "0", TR: "`+tr+`", CD: "`+claudeDir+`"}}
  on:  {cmd: ["`+agent+`", "{prompt}"], env: {FIX: "1", TR: "`+tr+`", CD: "`+claudeDir+`"}}
`), 0o644)
	cfg, err := LoadABConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	prices, _ := cost.Load(filepath.Join(root, "none.yml"))
	var rows []TrialResult
	for _, arm := range []string{"off", "on"} {
		r := RunTrial(tasks[0], arm, cfg.Arms[arm], 0, cfg, prices, claudeDir, root)
		if r.Error != "" || r.Exit != 0 || !r.CostKnown || r.TotalKRW <= 0 {
			t.Fatalf("%s: %+v", arm, r)
		}
		rows = append(rows, r)
	}
	if rows[0].Verified || !rows[1].Verified {
		t.Fatalf("verified by check command: %+v", rows)
	}
	if b, _ := os.ReadFile(filepath.Join(task, "repo", "add.txt")); string(b) != "broken\n" {
		t.Fatal("the task repository must not change")
	}
	ss := Summarize(rows, "off")
	if len(ss) != 2 || !ss[0].IsBaseline || ss[1].NonInferior != nil || ss[1].Rate != 1 {
		t.Fatalf("summary %+v", ss)
	}
	md := Markdown(ss)
	if !strings.Contains(md, "off (기준)") || !strings.Contains(md, "참고용") {
		t.Fatal(md)
	}
}

func TestSummaryGates(t *testing.T) {
	var rows []TrialResult
	for i := 0; i < 1000; i++ {
		rows = append(rows, TrialResult{Arm: "off", Trial: i, Verified: i < 750, TotalKRW: 1000, CostKnown: true})
		rows = append(rows, TrialResult{Arm: "on", Trial: i, Verified: i < 775, TotalKRW: 700, CostKnown: true})
	}
	ss := Summarize(rows, "off")
	on := ss[1]
	if on.SampleTooSmall || on.NonInferior != nil {
		t.Fatalf("unidentified repeated runs must not prove non-inferiority: %+v", on)
	}
	// the same rates on 40 trials are too uncertain to claim non-inferiority
	var small []TrialResult
	for i := 0; i < 40; i++ {
		small = append(small, TrialResult{Arm: "off", Verified: i < 30}, TrialResult{Arm: "on", Verified: i < 31})
	}
	if s := Summarize(small, "off")[1]; s.NonInferior != nil {
		t.Fatalf("40 trials: %+v", s)
	}
	if on.MeetsCostGoal != nil || on.CostReduction < 0.3 {
		t.Fatalf("cost: %+v", on)
	}
	// a clearly worse arm fails the non-inferiority gate
	var worse []TrialResult
	for i := 0; i < 40; i++ {
		worse = append(worse, TrialResult{Arm: "off", Verified: i < 36, CostKnown: true, TotalKRW: 1})
		worse = append(worse, TrialResult{Arm: "bad", Verified: i < 20, CostKnown: true, TotalKRW: 1})
	}
	if s := Summarize(worse, "off")[1]; s.NonInferior != nil {
		t.Fatalf("worse arm passed: %+v", s)
	}
}

func TestExampleABConfigResolvesRepositoryPaths(t *testing.T) {
	cfg, err := LoadABConfig(filepath.Join("..", "..", "bench", "ab.example.yml"))
	if err != nil {
		t.Fatal(err)
	}
	on, off := cfg.Arms["on"], cfg.Arms["off"]
	if on.Agent == "" || on.Agent != off.Agent || on.AgentVersion != off.AgentVersion || on.Model != off.Model {
		t.Fatalf("arms must declare the same agent, version and model: %+v %+v", on, off)
	}
	argv := ExpandArgv(on.Cmd, LiveTask{Dir: "/tmp/task", Prompt: "p"})
	wd, _ := os.Getwd()
	found := false
	for i, a := range argv {
		if a == "--plugin-dir" && i+1 < len(argv) {
			found = argv[i+1] == filepath.Join(wd, "plugins", "claude-code")
		}
	}
	if !found {
		t.Fatalf("the plugin directory must resolve from the start directory, not the trial copy: %v", argv)
	}
}
