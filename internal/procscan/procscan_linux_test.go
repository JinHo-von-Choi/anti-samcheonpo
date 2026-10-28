//go:build linux

package procscan

import (
	"os/exec"
	"testing"
	"time"
)

func TestLeftoverFindsAProcessStartedInTheWorkspace(t *testing.T) {
	root := t.TempDir()
	from := time.Now().Add(-2 * time.Second)
	cmd := exec.Command("sleep", "30")
	cmd.Dir = root
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	time.Sleep(50 * time.Millisecond)
	win := []Window{{From: from, To: time.Now().Add(time.Second)}}
	got := Leftover(root, win, nil, time.Now().Add(time.Second))
	if len(got) != 1 || got[0].PID != cmd.Process.Pid {
		t.Fatalf("got %+v", got)
	}
	if got := Leftover(root, []Window{{From: from.Add(-time.Hour), To: from.Add(-30 * time.Minute)}}, nil, time.Now().Add(time.Second)); len(got) != 0 {
		t.Fatalf("a process started outside every shell call is not the session's: %+v", got)
	}
	if got := Leftover(root, win, map[int]bool{cmd.Process.Pid: true}, time.Now().Add(time.Second)); len(got) != 0 {
		t.Fatalf("excluded: %+v", got)
	}
	if got := Leftover(t.TempDir(), win, nil, time.Now().Add(time.Second)); len(got) != 0 {
		t.Fatalf("another folder: %+v", got)
	}
}
