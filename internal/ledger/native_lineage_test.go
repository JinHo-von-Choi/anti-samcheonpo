package ledger

import (
	"path/filepath"
	"testing"
)

func TestNativeParentIdentityPersistsAndRejectsReparenting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.BindSessionParent("deepagents", "child", "parent", "project", "sdk"); err != nil {
		t.Fatal(err)
	}
	if err := d.BindSessionParent("deepagents", "child", "parent", "project", "sdk"); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []struct{ child, parent, project string }{
		{"child", "other", "project"}, {"parent", "child", "project"}, {"nested", "child", "other-project"},
	} {
		if err := d.BindSessionParent("deepagents", candidate.child, candidate.parent, candidate.project, "sdk"); err == nil {
			t.Fatalf("accepted conflict: %+v", candidate)
		}
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if parent, err := d.SessionParent("deepagents", "child", "project"); err != nil || parent != "parent" {
		t.Fatalf("parent=%q err=%v", parent, err)
	}
	if _, err := d.SessionParent("deepagents", "child", "other-project"); err == nil {
		t.Fatal("cross-project lineage leaked")
	}
}
