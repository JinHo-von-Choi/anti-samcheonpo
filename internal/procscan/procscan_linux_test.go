//go:build linux

package procscan

import (
	"os"
	"os/exec"
	"syscall"
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
	got := Leftover(root, win, nil, 0, time.Now().Add(time.Second))
	if len(got) != 1 || got[0].PID != cmd.Process.Pid {
		t.Fatalf("got %+v", got)
	}
	if got := Leftover(root, []Window{{From: from.Add(-time.Hour), To: from.Add(-30 * time.Minute)}}, nil, 0, time.Now().Add(time.Second)); len(got) != 0 {
		t.Fatalf("a process started outside every shell call is not the session's: %+v", got)
	}
	if got := Leftover(root, win, map[int]bool{cmd.Process.Pid: true}, 0, time.Now().Add(time.Second)); len(got) != 0 {
		t.Fatalf("excluded: %+v", got)
	}
	if got := Leftover(t.TempDir(), win, nil, 0, time.Now().Add(time.Second)); len(got) != 0 {
		t.Fatalf("another folder: %+v", got)
	}
}

func TestLeftoverLeavesWhatTheHostStillParents(t *testing.T) {
	root := t.TempDir()
	from := time.Now().Add(-2 * time.Second)
	cmd := exec.Command("sh", "-c", "sleep 30 & wait")
	cmd.Dir = root
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	time.Sleep(50 * time.Millisecond)
	win := []Window{{From: from, To: time.Now().Add(time.Second)}}
	if got := Leftover(root, win, nil, 0, time.Now().Add(time.Second)); len(got) == 0 {
		t.Fatal("with no known host the processes are candidates")
	}
	if got := Leftover(root, win, nil, os.Getpid(), time.Now().Add(time.Second)); len(got) != 0 {
		t.Fatalf("children and grandchildren of the host: %+v", got)
	}
	if got := Leftover(root, win, nil, 1, time.Now().Add(time.Second)); len(got) == 0 {
		t.Fatal("init parents every orphan and is never a host")
	}
}
