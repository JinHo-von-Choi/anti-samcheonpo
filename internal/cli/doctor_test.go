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

func TestDoctorReportsExperimentAndCapabilities(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("SAMCHEONPO_HOME", home)
	report := diagnose("claude", root, "/no/samcheonpo", filepath.Join(home, "ledger.db"), func(string) string { return "2.1.5" }, func() bool { return false })
	got := map[string]diagnostic{}
	for _, c := range report.Checks {
		got[c.ID] = c
	}
	if e := got["experiment"]; e.State != "ok" || !strings.Contains(e.Message, "꺼짐") {
		t.Fatalf("shipped default must report the experiment off: %+v", e)
	}
	if c := got["capabilities"]; !strings.Contains(c.Message, "실행 전 차단 가능") {
		t.Fatalf("a tested Claude Code reports pre-tool blocking: %+v", c)
	}
	report = diagnose("claude", root, "/no/samcheonpo", filepath.Join(home, "ledger.db"), func(string) string { return "" }, func() bool { return false })
	for _, c := range report.Checks {
		if c.ID == "capabilities" && !strings.Contains(c.Message, "실행 전 차단 불가") {
			t.Fatalf("an unverified agent is observation only: %+v", c)
		}
	}
}

func TestSlashCommandAcceptsUnquotedArguments(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	t.Setenv("SAMCHEONPO_NO_SPAWN", "1")
	c := slashCmd()
	c.SetArgs([]string{"rollback", "apply", "1a2b3c4d"})
	c.SetOut(new(strings.Builder))
	c.SetErr(new(strings.Builder))
	err := c.Execute()
	if err == nil || strings.Contains(err.Error(), "arg(s)") {
		t.Fatalf("/samcheonpo:rollback apply <ID> passes two words; only the missing daemon may fail it: %v", err)
	}
}

func TestSetFXKeepsUnreadablePriceTable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	p := filepath.Join(home, "prices.yml")
	orig := "models:\n  - model: m\n"
	_ = os.WriteFile(p, []byte(orig), 0o000)
	c := priceCmd()
	c.SetArgs([]string{"set-fx", "1400", "--from", "2026-10-01"})
	c.SetOut(new(strings.Builder))
	c.SetErr(new(strings.Builder))
	if err := c.Execute(); err == nil {
		t.Fatal("an unreadable price table is reported, not replaced")
	}
	_ = os.Chmod(p, 0o600)
	if b, _ := os.ReadFile(p); string(b) != orig {
		t.Fatalf("the price table is unchanged: %q", b)
	}
}
