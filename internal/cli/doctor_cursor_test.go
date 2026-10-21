package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/install"
)

func TestCursorDoctorInstallationStates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(home, "state"))
	db := filepath.Join(home, "missing.db")
	check := func(id string) diagnostic {
		t.Helper()
		for _, c := range cursorChecks(db) {
			if c.ID == id {
				return c
			}
		}
		t.Fatalf("missing %s", id)
		return diagnostic{}
	}
	if c := check("cursor_install"); c.State != "warning" {
		t.Fatal(c)
	}
	bin := filepath.Join(home, "samcheonpo")
	if err := os.WriteFile(bin, []byte("binary"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "hooks.json")
	if err := install.InstallCursor(bin, path, nil); err != nil {
		t.Fatal(err)
	}
	if c := check("cursor_install"); c.State != "ok" {
		t.Fatal(c)
	}
	if c := check("cursor_events"); c.State != "warning" {
		t.Fatal(c)
	}
	if c := check("cursor_delivery"); c.State != "warning" {
		t.Fatal(c)
	}
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if c := check("cursor_executable"); c.State != "error" {
		t.Fatal(c)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatal("doctor created ledger", err)
	}
	if err := install.UninstallCursor(nil); err != nil {
		t.Fatal(err)
	}
	if c := check("cursor_install"); c.State != "warning" {
		t.Fatal(c)
	}
}
