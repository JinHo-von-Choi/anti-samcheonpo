package bench

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testutil/fakeexe"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestMonitorConfigurationCannotEnableBlockingOrUnrequestedRules(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "monitor")
	for _, a := range []Arm{
		{Monitor: "none", MonitorExecutable: binary},
		{Monitor: "samcheonpo", MonitorExecutable: "relative"},
		{Monitor: "samcheonpo", MonitorExecutable: binary, MonitorRollout: "validated"},
		{Monitor: "samcheonpo", MonitorExecutable: binary, MonitorRollout: "shadow", RecommendRules: []string{"s2.behavior_loop"}},
		{Monitor: "samcheonpo", MonitorExecutable: binary, MonitorRollout: "recommend", RecommendRules: []string{"s8.budget"}},
	} {
		if _, err := monitorConfig(a); err == nil {
			t.Fatal("unsafe experimental monitor config accepted", a)
		}
	}
	c, err := monitorConfig(Arm{Monitor: "samcheonpo", MonitorExecutable: binary, MonitorRollout: "recommend", RecommendRules: []string{"s2.behavior_loop"}})
	if err != nil || c.Rollout.EscalateMaxBlocks != 0 || len(c.Rollout.RecommendRules) != 1 {
		t.Fatal(c.Rollout, err)
	}
}

func TestExplicitProductDefaultPreservesCurrentPolicy(t *testing.T) {
	a := Arm{Monitor: "samcheonpo", MonitorExecutable: filepath.Join(t.TempDir(), "monitor"), MonitorRollout: "default"}
	c, err := monitorConfig(a)
	if err != nil || !reflect.DeepEqual(c, config.Default()) {
		t.Fatal("default policy differs", err)
	}
	a.RecommendRules = []string{"s2.behavior_loop"}
	if _, err = monitorConfig(a); err == nil {
		t.Fatal("default policy silently includes candidate")
	}
}

func TestTrialPolicyIdentityPreservesControlsAcrossAllocatedPaths(t *testing.T) {
	c := config.Default()
	a, err := trialPolicyHash(c, []string{"/tmp/run-a/grader"})
	b, otherErr := trialPolicyHash(c, []string{"/tmp/run-b/grader"})
	if err != nil || otherErr != nil || a != b {
		t.Fatal("allocated grader path changed policy identity")
	}
	noGrader, _ := trialPolicyHash(c, nil)
	if a == noGrader {
		t.Fatal("grader protection omitted from identity")
	}
	c.Rollout.EscalateMaxBlocks++
	changed, _ := trialPolicyHash(c, []string{"/tmp/run-a/grader"})
	if a == changed {
		t.Fatal("policy change hidden by normalization")
	}
	c = config.Default()
	c.ProtectedPaths = []string{"/user/protected"}
	custom, _ := trialPolicyHash(c, nil)
	if custom == noGrader {
		t.Fatal("user policy path omitted from identity")
	}
}

func TestSelectedBuildStartsAndFinalizesInPrivateTrialHome(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("verified process termination currently requires Linux")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "samcheonpo")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, "../../cmd/samcheonpo")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build monitor: %v %s", err, out)
	}
	task := filepath.Join(root, "task")
	if err := os.MkdirAll(filepath.Join(task, "repo"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task, "repo", "add.txt"), []byte("broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	claudeDir := filepath.Join(root, "claude")
	if err := os.MkdirAll(filepath.Join(claudeDir, "proj"), 0700); err != nil {
		t.Fatal(err)
	}
	transcript, _ := filepath.Abs("../../testdata/claude/basic.jsonl")
	agent := fakeexe.Install(t, root, "agent", fakeexe.Spec{Behavior: "benchagent"})
	arm := Arm{Cmd: []string{agent, "fix"}, Env: map[string]string{"FIX": "1", "TR": transcript, "CD": claudeDir}, Monitor: "samcheonpo", MonitorExecutable: binary, MonitorRollout: "shadow"}
	prices, _ := cost.Load("")
	r := RunTrial(LiveTask{Name: "fix", Dir: task, Check: "grep -q fixed add.txt"}, "shadow", arm, 1, ABConfig{TimeoutSec: 20}, prices, claudeDir, root)
	if r.Error != "" || r.MonitorObservationError == "" || r.MonitorKRW != nil || len(r.MonitorBinarySHA256) != 64 || len(r.MonitorConfigSHA256) != 64 || r.TotalDurationMS < r.DurationMS+r.GraderDurationMS {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(task, "repo", "add.txt")); err != nil {
		t.Fatal("source task modified")
	}
}
