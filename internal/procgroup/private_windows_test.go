//go:build windows

package procgroup

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestMkdirAllPrivateLeavesOnlyUserAndSystem(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	if err := MkdirAllPrivate(dir); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if ctrl&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("the directory still inherits access from its parent")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		t.Fatalf("no access list: %v", err)
	}
	if acl.AceCount != 2 {
		t.Fatalf("want exactly the user and the system account, got %d entries", acl.AceCount)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}
