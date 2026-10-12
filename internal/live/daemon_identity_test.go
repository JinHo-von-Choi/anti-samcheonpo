package live

import "testing"

func TestExplicitUnknownSessionCannotFallBackToActiveSession(t *testing.T) {
	s := &Session{ID: "active", Root: "/project"}
	d := &Daemon{sessions: map[string]*Session{"active": s}}
	if d.latest("/project", "missing") != nil {
		t.Fatal("unknown explicit session selected a different active session")
	}
	if d.latest("/project", "active") != s || d.latest("/project", "") != s {
		t.Fatal("valid session selection broken")
	}
}
