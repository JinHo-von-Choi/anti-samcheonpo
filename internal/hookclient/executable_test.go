package hookclient

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutableUsesExecutableSiblingAndKeepsDaemonSeparate(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "samcheonpo")
	helper := filepath.Join(dir, "samcheonpo-hook")
	if got := Executable(main); got != main {
		t.Fatal(got)
	}
	if err := os.WriteFile(helper, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := Executable(main); got != main {
		t.Fatal("nonexecutable sibling selected")
	}
	if err := os.Chmod(helper, 0700); err != nil {
		t.Fatal(err)
	}
	if got := Executable(main); got != helper {
		t.Fatal(got)
	}
	if got := daemonExecutable(helper); got != main {
		t.Fatal("helper would spawn itself", got)
	}
	if got := daemonExecutable(main); got != main {
		t.Fatal(got)
	}
}
