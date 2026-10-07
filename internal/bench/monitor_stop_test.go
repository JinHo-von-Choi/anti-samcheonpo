package bench

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestMonitorTerminationMustBeConfirmed(t *testing.T) {
	home := t.TempDir()
	if !stopDaemon(home) {
		t.Fatal("absent monitor is not pending")
	}
	_ = os.Mkdir(filepath.Join(home, "run"), 0700)
	for _, pid := range []string{"1", "invalid", strconv.Itoa(os.Getpid())} {
		if err := os.WriteFile(filepath.Join(home, "run", "daemon.lock"), []byte(pid), 0600); err != nil {
			t.Fatal(err)
		}
		if stopDaemon(home) {
			t.Fatalf("unowned/invalid process %q accepted", pid)
		}
	}
}
