package procgroup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testutil/fakeexe"
)

func TestShellKeepsQuotedArguments(t *testing.T) {
	exe := fakeexe.Install(t, t.TempDir(), "echoargs", fakeexe.Spec{Behavior: "argsecho"})
	cmd := Shell(`"` + exe + `" "a b" c`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v", ShellName(), err)
	}
	if got := strings.TrimSpace(string(out)); got != "a b|c" {
		t.Fatalf("%s: arguments arrived as %q", ShellName(), got)
	}
}

func TestShellRunsCommandThatEndsInAQuote(t *testing.T) {
	exe := fakeexe.Install(t, t.TempDir(), "quoted", fakeexe.Spec{Stdout: "ran\n"})
	for _, command := range []string{`"` + exe + `"`, `"` + filepath.ToSlash(exe) + `"`, `echo "a" "b"`} {
		out, err := Shell(command).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v: %s", ShellName(), command, err, out)
		}
	}
}

func TestKillEndsEveryDescendant(t *testing.T) {
	exe := fakeexe.Install(t, t.TempDir(), "spawn", fakeexe.Spec{Behavior: "spawner"})
	cmd := Shell(`"` + exe + `"`)
	Set(cmd)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := Start(cmd); err != nil {
		t.Fatal(err)
	}
	defer Release(cmd)
	time.Sleep(700 * time.Millisecond)
	if err := Kill(cmd); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a descendant survived the kill and still holds the output pipe")
	}
}

func TestReleaseEndsOrphans(t *testing.T) {
	exe := fakeexe.Install(t, t.TempDir(), "spawn", fakeexe.Spec{Behavior: "spawner", Option: "orphan"})
	cmd := Shell(`"` + exe + `"`)
	Set(cmd)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := Start(cmd); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	Release(cmd)
	_ = Kill(cmd)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("an orphan outlived the group")
	}
}

func TestHardLinkedCountsNames(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(a)
	if err != nil {
		t.Fatal(err)
	}
	if HardLinked(a, st) {
		t.Fatal("a file with one name is not hard-linked")
	}
	if err := os.Link(a, b); err != nil {
		t.Skip("hard links unavailable:", err)
	}
	st, err = os.Lstat(a)
	if err != nil {
		t.Fatal(err)
	}
	if !HardLinked(a, st) {
		t.Fatal("a file with two names is hard-linked")
	}
}

func TestRenameWaitsForAReaderToLetGo(t *testing.T) {
	dir := t.TempDir()
	dst, tmp := filepath.Join(dir, "state.json"), filepath.Join(dir, "state.json.tmp")
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		reader.Close()
	}()
	if err := Rename(tmp, dst); err != nil {
		t.Fatalf("rename over a file that was open for 300ms: %v", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Fatalf("content %q", b)
	}
}
