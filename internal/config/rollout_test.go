package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestProjectCannotPromoteRolloutOrForgeEvidence(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	hash := strings.Repeat("a", 64)
	projectPath := filepath.Join(project, ".samcheonpo.yml")
	_ = os.WriteFile(projectPath, []byte("rollout:\n  mode: validated\n  validated_rules: {new: "+hash+"}\n"), 0600)
	cfg, _, err := Load(project)
	if err != nil || cfg.Rollout.Mode != "recommend" || len(cfg.Rollout.ValidatedRules) != 0 {
		t.Fatalf("project escalated: %+v %v", cfg.Rollout, err)
	}
	_ = os.WriteFile(filepath.Join(home, "config.yml"), []byte("rollout:\n  mode: validated\n  validated_rules: {approved: "+hash+"}\n"), 0600)
	cfg, _, err = Load(project)
	if err != nil || cfg.Rollout.Mode != "validated" || cfg.Rollout.ValidatedRules["approved"] != hash || cfg.Rollout.ValidatedRules["new"] != "" {
		t.Fatalf("evidence escalation: %+v %v", cfg.Rollout, err)
	}
	_ = os.WriteFile(projectPath, []byte("rollout: {mode: shadow}\n"), 0600)
	cfg, _, err = Load(project)
	if err != nil || cfg.Rollout.Mode != "shadow" {
		t.Fatal("cannot restrict rollout")
	}
}

func TestProjectCannotRaiseEscalationCap(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	projectPath := filepath.Join(project, ".samcheonpo.yml")
	_ = os.WriteFile(projectPath, []byte("rollout: {escalate_max_blocks: 50}\n"), 0600)
	if cfg, _, err := Load(project); err != nil || cfg.Rollout.EscalateMaxBlocks != 3 {
		t.Fatalf("project raised the cap: %d %v", cfg.Rollout.EscalateMaxBlocks, err)
	}
	_ = os.WriteFile(projectPath, []byte("rollout: {escalate_max_blocks: 0}\n"), 0600)
	if cfg, _, err := Load(project); err != nil || cfg.Rollout.EscalateMaxBlocks != 0 {
		t.Fatalf("project cannot turn escalation off: %d %v", cfg.Rollout.EscalateMaxBlocks, err)
	}
	_ = os.WriteFile(projectPath, []byte("rollout: {escalate_max_blocks: -1}\n"), 0600)
	if _, _, err := Load(project); err == nil {
		t.Fatal("negative cap accepted")
	}
}

func TestContractDraftDefaultsOffAndValidates(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	if cfg, _, err := Load(project); err != nil || cfg.Contract.Draft != "off" {
		t.Fatalf("observation-only is the default: %q %v", cfg.Contract.Draft, err)
	}
	projectPath := filepath.Join(project, ".samcheonpo.yml")
	_ = os.WriteFile(projectPath, []byte("contract: {draft: on}\n"), 0600)
	if cfg, _, err := Load(project); err != nil || cfg.Contract.Draft != "on" {
		t.Fatalf("drafts can be turned on: %q %v", cfg.Contract.Draft, err)
	}
	_ = os.WriteFile(projectPath, []byte("contract: {draft: sometimes}\n"), 0600)
	if _, _, err := Load(project); err == nil {
		t.Fatal("an unknown draft mode is rejected")
	}
}

func TestProjectCanAddButNotRemoveShadowRules(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	cfg, _, err := Load(project)
	if err != nil || !slices.Contains(cfg.Rollout.ShadowRules, "s1.review_repeat") || !slices.Contains(cfg.Rollout.ShadowRules, "s1.verify_after_docs") {
		t.Fatalf("new rules start in shadow: %v %v", cfg.Rollout.ShadowRules, err)
	}
	_ = os.WriteFile(filepath.Join(project, ".samcheonpo.yml"), []byte("rollout: {shadow_rules: [s3.out_of_scope]}\n"), 0600)
	cfg, _, err = Load(project)
	if err != nil || !slices.Contains(cfg.Rollout.ShadowRules, "s3.out_of_scope") || !slices.Contains(cfg.Rollout.ShadowRules, "s1.review_repeat") {
		t.Fatalf("a project adds shadow rules but keeps the user's: %v %v", cfg.Rollout.ShadowRules, err)
	}
}

func TestReliabilityShadowRequiresUserOptIn(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	rules := []string{"s1.explicit_waiting", "s8.progress_stall"}
	cfg, _, err := Load(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if !slices.Contains(cfg.Rollout.ShadowRules, r) {
			t.Fatal("not shadow by default", r)
		}
	}
	if err := os.WriteFile(filepath.Join(project, ".samcheonpo.yml"), []byte("rollout: {shadow_rules: []}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = Load(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if !slices.Contains(cfg.Rollout.ShadowRules, r) {
			t.Fatal("project opted user into delivery", r)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "config.yml"), []byte("rollout: {mode: recommend, shadow_rules: [s1.review_repeat, s1.verify_after_docs]}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = Load(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if !slices.Contains(cfg.Rollout.ShadowRules, r) {
			t.Fatal("legacy user config implicitly opted in", r)
		}
	}
	if err := os.WriteFile(filepath.Join(project, ".samcheonpo.yml"), []byte("rollout: {shadow_rules: [], recommend_rules: [s1.explicit_waiting, s8.progress_stall]}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = Load(project)
	if err != nil || len(cfg.Rollout.RecommendRules) != 0 || !slices.Contains(cfg.Rollout.ShadowRules, rules[0]) {
		t.Fatalf("project granted recommendation: %+v %v", cfg.Rollout, err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.yml"), []byte("rollout: {recommend_rules: [s1.explicit_waiting]}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = Load(project)
	if err != nil || slices.Contains(cfg.Rollout.ShadowRules, rules[0]) || !slices.Contains(cfg.Rollout.ShadowRules, rules[1]) {
		t.Fatalf("scoped user opt-in ignored: %+v %v", cfg.Rollout, err)
	}
	if err := os.WriteFile(filepath.Join(project, ".samcheonpo.yml"), []byte("rollout: {shadow_rules: [s1.explicit_waiting]}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = Load(project)
	if err != nil || !slices.Contains(cfg.Rollout.ShadowRules, rules[0]) {
		t.Fatalf("project cannot restrict opted-in rule: %+v %v", cfg.Rollout, err)
	}
}
