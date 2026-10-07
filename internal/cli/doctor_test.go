package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorInCleanHomeDoesNotCreateStateOrRequireKeys(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("SAMCHEONPO_HOME", home)
	t.Setenv("ANTHROPIC_API_KEY", "")
	report := diagnose("claude", root, "/no/samcheonpo", filepath.Join(home, "ledger.db"), func(string) string { return "" }, func() bool { return false })
	var judge, ledger bool
	for _, c := range report.Checks {
		if c.State == "error" {
			t.Fatalf("clean offline install reported fatal error: %+v", c)
		}
		if c.ID == "judge" && c.State == "ok" {
			judge = true
		}
		if c.ID == "ledger" && c.State == "warning" {
			ledger = true
		}
	}
	if !judge || !ledger {
		t.Fatalf("%+v", report)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("doctor created state", err)
	}
}

func TestDoctorDoesNotPrintMalformedConfigSecrets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, ".samcheonpo.yml"), []byte("checkpoint: {timeout_sec: TOP_SECRET_TOKEN}"), 0600); err != nil {
		t.Fatal(err)
	}
	report := diagnose("claude", root, "/no/samcheonpo", filepath.Join(root, "none.db"), func(string) string { return "2.1.0" }, func() bool { return false })
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "TOP_SECRET_TOKEN") {
		t.Fatal("configuration secret leaked")
	}
	found := false
	for _, c := range report.Checks {
		if c.ID == "config" && c.State == "error" {
			found = true
		}
	}
	if !found {
		t.Fatal("invalid config hidden")
	}
}
