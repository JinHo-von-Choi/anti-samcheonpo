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
