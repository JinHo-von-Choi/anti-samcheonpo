package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"testing"
)

func TestLinkedAgentRestoresIntentWithoutInheritingPassOrGrantingDraftAuthority(t *testing.T) {
	s, d := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "원래 작업의 목표"})
	link := intent.SessionLink{TaskID: s.task.ID, Agent: "codex", SessionID: "next-agent", ParentAgent: s.Agent, ParentSessionID: s.ID}
	if err := s.db.LinkTaskSession(link); err != nil {
		t.Fatal(err)
	}
	next, err := newSession(link.SessionID, link.Agent, s.Root, "", s.db, s.prices, adapter.Profiles["codex"].Caps)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Finalize()
	if next.task == nil || next.task.ID != s.task.ID || next.intentRevision.Goal != "원래 작업의 목표" || next.taskLink.ParentSessionID != s.ID {
		t.Fatal("linked agent lost source intent")
	}
	if next.eng.Accepted || len(next.checks) > 0 || len(next.checkpoint(false)) > 0 {
		t.Fatal("draft handoff granted authority or inherited a pass")
	}
	next.onPrompt(HookInput{Prompt: "추가 설명"})
	_, r, restored, err := s.db.SessionIntent(link.Agent, link.SessionID)
	if err != nil || r.Number != 2 || r.Goal != "원래 작업의 목표" || r.Source.SessionID != link.SessionID || restored != link {
		t.Fatalf("%+v %+v %v", r, restored, err)
	}
	if _, err := d.session(s.ID, "codex", s.Root, ""); err == nil {
		t.Fatal("live cross-agent collision reused another session")
	}
	if _, err := next.Finalize(); err != nil {
		t.Fatal(err)
	}
	var agent string
	if err := s.db.QueryRow(`SELECT agent FROM session WHERE id=?`, link.SessionID).Scan(&agent); err != nil || agent != "codex" {
		t.Fatalf("final receipt attributed to %q: %v", agent, err)
	}
	if collision, err := newSession(link.SessionID, "claude", s.Root, "", s.db, s.prices, adapter.Profiles["claude"].Caps); err == nil {
		collision.Finalize()
		t.Fatal("persisted cross-agent collision accepted")
	}
}
