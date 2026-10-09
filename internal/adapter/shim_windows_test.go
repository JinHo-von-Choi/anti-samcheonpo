//go:build windows

package adapter

import (
	"os"
	"path/filepath"
	"testing"
)

// Agents installed with npm are batch shims, not executables.
func TestVersionProbeRunsBatchShim(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(bin, "claude.cmd"), []byte("@echo off\r\necho 2.1.0\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ProbeVersion("claude"); got != "2.1.0" {
		t.Fatalf("version of a batch shim: %q", got)
	}
}
