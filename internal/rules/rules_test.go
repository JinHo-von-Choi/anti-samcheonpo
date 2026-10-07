package rules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltinCases(t *testing.T) {
	s, err := Parse(builtin)
	if err != nil {
		t.Fatal(err)
	}
	if bad := s.Check(); len(bad) > 0 {
		t.Fatal(bad)
	}
}

func TestUserFileExtendsAndBadFileIgnored(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", dir)
	good := "version: 1\nverify_commands:\n  patterns: ['^just test\\b']\n  cases:\n    match: ['just test']\n    no_match: ['just build']\n"
	if err := os.WriteFile(filepath.Join(dir, "rules.yml"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	s, warns := Load()
	if len(warns) != 0 || !s.VerifyCommands.Match("just test") {
		t.Fatalf("user rules extend the built-ins: %v", warns)
	}
	bad := "version: 1\nverify_commands:\n  patterns: ['^just']\n  cases:\n    no_match: ['just build']\n"
	_ = os.WriteFile(filepath.Join(dir, "rules.yml"), []byte(bad), 0o644)
	s, warns = Load()
	if len(warns) != 1 || s.VerifyCommands.Match("just build") {
		t.Fatalf("a user file failing its own cases is ignored: %v", warns)
	}
}
