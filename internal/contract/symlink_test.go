package contract

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An acceptance made from one spelling of a project path holds for the others,
// and one made before signatures were canonical (signed over the typed path)
// still holds.
func TestAcceptanceHoldsAcrossSymlinkedSpellings(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	base := t.TempDir()
	real := filepath.Join(base, "real")
	project := filepath.Join(real, "proj")
	if err := os.MkdirAll(Dir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symbolic links unavailable:", err)
	}
	viaLink := filepath.Join(link, "proj")
	if err := os.WriteFile(Path(project), []byte("goal: x\ndone:\n  - check: \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, raw, err := Load(viaLink)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Accept(viaLink, c, raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{project, viaLink} {
		c2, raw2, err := Load(spelling)
		if err != nil {
			t.Fatal(err)
		}
		if got := CurrentState(spelling, c2, raw2); got.State != StateAccepted {
			t.Errorf("read through %q: state %q", spelling, got.State)
		}
	}

	// a record signed over the path as typed, as older versions wrote it
	acc := LoadAcceptance(project)
	acc.MAC = ""
	legacy, err := acceptanceMACFor(viaLink, acc)
	if err != nil {
		t.Fatal(err)
	}
	acc.MAC = legacy
	if !validMAC(viaLink, acc) {
		t.Error("a signature over the typed path must still verify")
	}
}
