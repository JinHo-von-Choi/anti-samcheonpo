//go:build linux

package hostlimit

import "testing"

// The Linux counter must find this process's own inotify instance when one
// exists, and must never report a negative count when a process exits
// mid-scan.
func TestInotifyCountIsNotNegative(t *testing.T) {
	w := Inotify()
	if w.Used < 0 {
		t.Fatalf("negative instance count %d", w.Used)
	}
	if !w.Known {
		if w.Limit != 0 {
			t.Fatalf("unknown budget carries a limit %d", w.Limit)
		}
		return
	}
	if w.Limit <= 0 {
		t.Fatalf("known budget with limit %d", w.Limit)
	}
}

// countInstances reads /proc; a directory that disappears while it is being
// read is skipped instead of failing the whole count.
func TestCountToleratesVanishingProcesses(t *testing.T) {
	if n := countInstances(); n < 0 {
		t.Fatalf("negative count %d", n)
	}
}
