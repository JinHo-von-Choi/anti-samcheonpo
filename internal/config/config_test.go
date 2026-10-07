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
