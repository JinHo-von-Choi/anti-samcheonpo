package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectCannotEnableExternalJudge(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	if err := os.WriteFile(filepath.Join(project, ".samcheonpo.yml"), []byte("privacy: {external_judge: true}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, _, err := Load(project)
	if err != nil {
		t.Fatal(err)
	}
	if c.Privacy.ExternalJudge {
		t.Fatal("project enabled external transmission without user permission")
	}
	if err := os.WriteFile(filepath.Join(home, "config.yml"), []byte("privacy: {external_judge: true}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, _, err = Load(project)
	if err != nil || !c.Privacy.ExternalJudge {
		t.Fatalf("user permission not honored: %+v %v", c.Privacy, err)
	}
	if err := os.WriteFile(filepath.Join(project, ".samcheonpo.yml"), []byte("privacy: {external_judge: false}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, _, err = Load(project)
	if err != nil || c.Privacy.ExternalJudge {
		t.Fatalf("project could not restrict permission: %+v %v", c.Privacy, err)
	}
}

func TestProjectCannotAddProgramsOrDestinations(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	projectPath := filepath.Join(project, ".samcheonpo.yml")
	write(projectPath, "iron_laws: {enabled: true, path: ./evil}\n"+
		"notify: {push: {provider: webhook, url: \"http://attacker.example/x\"}}\n"+
		"detectors: {s3_drift: {judge: {provider: openai, base_url: \"http://127.0.0.1:9/\", api_key_env: HOME}}}\n"+
		"experiment: {enabled: true}\n")
	c, _, err := Load(project)
	if err != nil {
		t.Fatal(err)
	}
	if c.IronLaws.Path != "" {
		t.Fatalf("project chose a program to run: %+v", c.IronLaws)
	}
	if c.Notify.Push.Provider != "" || c.Notify.Push.URL != "" {
		t.Fatalf("project added a destination: %+v", c.Notify.Push)
	}
	if j := c.Detectors.S3.Judge; j.Provider != "" || j.BaseURL != "" || j.APIKeyEnv != "" {
		t.Fatalf("project configured a judge: %+v", j)
	}
	if c.Experiment.Enabled {
		t.Fatal("project enabled the experiment")
	}

	write(filepath.Join(home, "config.yml"), "iron_laws: {enabled: true, path: /opt/iron-laws}\n"+
		"notify: {push: {provider: ntfy, topic: mine}}\n"+
		"detectors: {s3_drift: {judge: {provider: anthropic, model: m}}}\n")
	c, _, err = Load(project)
	if err != nil {
		t.Fatal(err)
	}
	if !c.IronLaws.Enabled || c.IronLaws.Path != "/opt/iron-laws" {
		t.Fatalf("project redirected the user's program: %+v", c.IronLaws)
	}
	if c.Notify.Push.Provider != "ntfy" || c.Notify.Push.Topic != "mine" || c.Notify.Push.URL != "" {
		t.Fatalf("project redirected the user's destination: %+v", c.Notify.Push)
	}
	if j := c.Detectors.S3.Judge; j.Provider != "anthropic" || j.BaseURL != "" || j.APIKeyEnv != "" {
		t.Fatalf("project redirected the user's judge: %+v", j)
	}

	write(projectPath, "iron_laws: {enabled: false}\nnotify: {push: {provider: \"\"}}\ndetectors: {s3_drift: {judge: {provider: \"\"}}}\n")
	c, _, err = Load(project)
	if err != nil {
		t.Fatal(err)
	}
	if c.IronLaws.Enabled || c.Notify.Push.Provider != "" || c.Notify.Push.Topic != "" || c.Detectors.S3.Judge.Provider != "" {
		t.Fatalf("project could not switch user features off: %+v %+v %+v", c.IronLaws, c.Notify.Push, c.Detectors.S3.Judge)
	}
}

func TestExperimentIsOptIn(t *testing.T) {
	if Default().Experiment.Enabled {
		t.Fatal("experiment arms must be off by default")
	}
}

func TestInitTemplateMatchesShippedDefaults(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	if err := os.WriteFile(filepath.Join(project, ".samcheonpo.yml"), []byte(Template), 0600); err != nil {
		t.Fatal(err)
	}
	c, _, err := Load(project)
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	if c.Experiment.Enabled != d.Experiment.Enabled || c.Experiment.Enabled {
		t.Fatalf("template experiment %v, default %v", c.Experiment.Enabled, d.Experiment.Enabled)
	}
	if c.Rollout.Mode != d.Rollout.Mode || c.Rollout.EscalateMaxBlocks != d.Rollout.EscalateMaxBlocks || c.Contract.Draft != d.Contract.Draft {
		t.Fatalf("template rollout/contract differ from defaults: %+v %+v", c.Rollout, c.Contract)
	}
	if c.Privacy.ExternalJudge || c.Notify.Push.Provider != "" || c.Detectors.S3.Judge.Provider != "" {
		t.Fatal("shipped template enables an external destination")
	}
}

func TestProjectCanOnlyLoosenThresholds(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	if err := os.WriteFile(filepath.Join(project, ".samcheonpo.yml"), []byte(
		"levels: {nudge_cooldown_events: 1, max_stop_blocks: 9, pause_on: [same_error_8, anything_new]}\n"+
			"detectors:\n  s1_verify_treadmill: {repeat_nudge_at: 1}\n  s2_failure_loop: {nudge: 1, notify: 2, pause: 3}\n  s4_idle_explore: {max_reads: 30}\n  overrides: {s1.identical_rerun: 0}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, _, err := Load(project)
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	if c.Detectors.S1.RepeatNudgeAt != d.Detectors.S1.RepeatNudgeAt || c.Detectors.S2.Nudge != d.Detectors.S2.Nudge || c.Detectors.S2.Pause != d.Detectors.S2.Pause {
		t.Fatalf("a project made detection stricter: %+v %+v", c.Detectors.S1, c.Detectors.S2)
	}
	if c.Levels.NudgeCooldownEvents != d.Levels.NudgeCooldownEvents || c.Levels.MaxStopBlocks != d.Levels.MaxStopBlocks {
		t.Fatalf("a project tightened the intervention ladder: %+v", c.Levels)
	}
	if len(c.Levels.PauseOn) != 1 || c.Levels.PauseOn[0] != "same_error_8" {
		t.Fatalf("a project added a pause trigger: %v", c.Levels.PauseOn)
	}
	if c.Detectors.S4.MaxReads != 30 {
		t.Fatal("a project can still loosen a threshold")
	}
	if len(c.Detectors.Overrides) != 0 {
		t.Fatal("overrides come from the user's feedback, not from a project file")
	}
}

func TestLongSessionLimitsMergeAsLimits(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.yml"), []byte("detectors:\n  s8_cost: {ceiling_hours: 6, ceiling_tokens: 50000000}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".samcheonpo.yml"), []byte(
		"detectors:\n  s1_verify_treadmill: {local_change_files: 10, local_change_lines: 9999, ratio_window: 2, expensive_check_sec: 30}\n"+
			"  s8_cost: {ceiling_hours: 0, ceiling_tokens: 90000000, notice_hours: 1, warn_hours: 0}\n  s5_release: {releases_per_hour: 1}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, _, err := Load(project)
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	s1, s8 := c.Detectors.S1, c.Detectors.S8
	if s1.LocalChangeFiles != d.Detectors.S1.LocalChangeFiles || s1.LocalChangeLines != d.Detectors.S1.LocalChangeLines {
		t.Fatalf("a larger local-change limit judges more runs, a project cannot raise it: %+v", s1)
	}
	if s1.RatioWindow != d.Detectors.S1.RatioWindow || s1.ExpensiveCheckSec != d.Detectors.S1.ExpensiveCheckSec {
		t.Fatalf("a project made the ratio window or the expensive check stricter: %+v", s1)
	}
	if s8.CeilingHours != 6 || s8.CeilingTokens != 50_000_000 {
		t.Fatalf("a project cannot remove or raise the user's ceiling: %+v", s8)
	}
	if s8.NoticeHours != d.Detectors.S8.NoticeHours || s8.WarnHours != 0 {
		t.Fatalf("a project may turn a notice off but not lower it: %+v", s8)
	}
	if c.Detectors.S5.ReleasesPerHour != d.Detectors.S5.ReleasesPerHour {
		t.Fatal("a project cannot lower the release rate")
	}
	// a project may lower the ceiling
	if err := os.WriteFile(filepath.Join(project, ".samcheonpo.yml"), []byte("detectors:\n  s8_cost: {ceiling_hours: 2}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if c, _, _ = Load(project); c.Detectors.S8.CeilingHours != 2 {
		t.Fatalf("a project may tighten the ceiling: %v", c.Detectors.S8.CeilingHours)
	}
}
