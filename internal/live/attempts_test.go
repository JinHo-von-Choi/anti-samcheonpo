package live

import (
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
)

func TestEditFPIgnoresIndentationOnly(t *testing.T) {
	a := editFP(event.PatchFile{Path: "a.py", Added: []string{"    return a + b"}, Removed: []string{"    return a - b"}})
	b := editFP(event.PatchFile{Path: "a.py", Added: []string{"return a + b", ""}, Removed: []string{"\treturn a - b"}})
	if a == "" || a != b {
		t.Fatal("the same change with other indentation is the same edit")
	}
	if a == editFP(event.PatchFile{Path: "b.py", Added: []string{"return a + b"}, Removed: []string{"return a - b"}}) {
		t.Fatal("another file is another edit")
	}
	if editFP(event.PatchFile{Path: "a.py"}) != "" {
		t.Fatal("an empty patch has no fingerprint")
	}
}

func TestFailedAttemptsCarryAcrossSessionsOfOneRevision(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.task, s.intentRevision = &intent.Task{ID: "task-1"}, &intent.Revision{Number: 2}
	exit := 1
	patch := event.PatchFile{Path: "a.py", Added: []string{"return a + b"}, Removed: []string{"return a - b"}}
	s.trackAttempts(&event.Event{Seq: 1, Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Patch: []event.PatchFile{patch}})
	s.trackAttempts(&event.Event{Seq: 2, Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify, Cmd: "pytest", CmdNorm: "pytest", ExitCode: &exit, FailedTests: []string{"t::x"}})
	if got, _ := s.db.FailedAttempts("task-1", 2); len(got) != 1 || got[0].Path != "a.py" {
		t.Fatalf("a failing verification records the edits before it: %+v", got)
	}
	// a later session on the same revision, e.g. after a restart or handoff
	s.ID, s.failedEdits = "another-session", nil
	s.loadFailedAttempts()
	pre := &event.Event{Seq: 9, Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Patch: []event.PatchFile{patch}}
	v := s.attemptPre(pre)
	if v == nil || v.Rule != "s2.attempt_repeat" || v.Facts["same_session"] != false {
		t.Fatalf("the failed edit is recognized in a later session: %+v", v)
	}
	// a new revision is a new goal
	s.intentRevision = &intent.Revision{Number: 3}
	s.loadFailedAttempts()
	if s.attemptPre(pre) != nil {
		t.Fatal("attempts from another revision do not apply")
	}
	// a passing verification discards pending edits
	s.trackAttempts(&event.Event{Seq: 10, Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Patch: []event.PatchFile{patch}})
	zero := 0
	s.trackAttempts(&event.Event{Seq: 11, Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify, Cmd: "pytest", CmdNorm: "pytest", ExitCode: &zero})
	if got, _ := s.db.FailedAttempts("task-1", 3); len(got) != 0 {
		t.Fatalf("a passing verification records nothing: %+v", got)
	}
}
