//go:build windows

package procgroup

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

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
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	// Windows may store one grant as several entries (this folder, inherit
	// only), so the check is on who is named, not on how many entries there are
	named := map[string]bool{}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		switch {
		case sid.Equals(user.User.Sid), sid.Equals(system):
			named[sid.String()] = true
		default:
			t.Errorf("unexpected principal in the access list: %s", sid)
		}
	}
	if !named[user.User.Sid.String()] || !named[system.String()] {
		t.Errorf("the user and the system account must both be named: %v", named)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}
