//go:build windows

package contract

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Windows paths are case-insensitive, so an acceptance made from one spelling
// of the project path must hold for the others.
func TestAcceptanceHoldsForAnotherSpellingOfTheProjectPath(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	project := t.TempDir()
	if err := os.MkdirAll(Dir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(project), []byte("goal: x\ndone:\n  - check: \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, raw, err := Load(project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Accept(project, c, raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{strings.ToLower(project), strings.ToUpper(project)} {
		c2, raw2, err := Load(spelling)
		if err != nil {
			t.Fatal(err)
		}
		if got := CurrentState(spelling, c2, raw2); got.State != StateAccepted {
			t.Errorf("accepted from %q, read as %q: state %q", project, spelling, got.State)
		}
	}
}
