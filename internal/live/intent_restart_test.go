package live

import (
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
)

func TestIntentSourcesAndFollowupsSurviveSessionRestart(t *testing.T) {
	s, d := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "로그인 화면을 만들어 줘"})
	s.onPrompt(HookInput{Prompt: "버튼은 파란색으로"})
	task, r, _, err := s.db.SessionIntent(s.Agent, s.ID)
	if err != nil || r.Number != 2 || r.Kind != intent.Followup || r.Goal != "로그인 화면을 만들어 줘" || r.Message != "버튼은 파란색으로" || r.Source.Origin != intent.User || !strings.HasPrefix(r.Source.EventID, "event:") {
		t.Fatalf("%+v %v", r, err)
	}
	s.onPrompt(HookInput{Prompt: "목표 변경: 가입 화면으로"})
	_, r, _, err = s.db.SessionIntent(s.Agent, s.ID)
	if err != nil || r.Number != 3 || r.Goal != "가입 화면으로" || r.Kind != intent.Redirect {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := s.Finalize(); err != nil {
		t.Fatal(err)
	}
	d.drop(s.ID)
	_, resumed, err := d.dispatch(s.Agent, "SessionStart", HookInput{SessionID: s.ID, Cwd: s.Root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = resumed.Finalize() })
	resumed.mu.Lock()
	if resumed.task == nil || resumed.task.ID != task.ID || resumed.intentRevision.Number != 3 || resumed.acc.State != contract.StateDraft || resumed.eng.Accepted {
		t.Fatal("restore lost identity or restored authority")
	}
	resumed.mu.Unlock()
	text, err := d.command(CommandInput{Name: "card", Session: s.ID})
	if err != nil || !strings.Contains(text, "가입 화면으로") || !strings.Contains(text, "개정 3") || !strings.Contains(text, "event:") {
		t.Fatalf("%s %v", text, err)
	}
	resumed.onPrompt(HookInput{Prompt: "모바일에서도 보이게"})
	_, r, _, err = s.db.SessionIntent(s.Agent, s.ID)
	if err != nil || r.Number != 4 || r.Kind != intent.Followup || r.Goal != "가입 화면으로" {
		t.Fatalf("resumed followup replaced goal: %+v %v", r, err)
	}
}

func TestEditCommandWritesAttributableRevisionWithoutCheckAuthority(t *testing.T) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "true")
	if _, err := d.command(CommandInput{Name: "edit", Session: s.ID, Arg: "new goal"}); err != nil {
		t.Fatal(err)
	}
	_, r, _, err := s.db.SessionIntent(s.Agent, s.ID)
	if err != nil || r.Source.Origin != intent.User || r.Kind != intent.Redirect || !r.Draft || r.Goal != "new goal" {
		t.Fatalf("%+v %v", r, err)
	}
	if results := s.checkpoint(false); results != nil {
		t.Fatal("persisting intent granted execution")
	}
}
