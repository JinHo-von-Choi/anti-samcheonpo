// Package fsx holds file system helpers for tests.
package fsx

import (
	"os"
	"runtime"
	"testing"
)

// Symlink makes a symbolic link for a test. Creating one on Windows takes a
// privilege (an elevated process or Developer Mode) that a standard user lacks,
// so the test is skipped there instead of failing.
func Symlink(t testing.TB, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symbolic links need a privilege this process lacks: %v", err)
		}
		t.Fatal(err)
	}
}
