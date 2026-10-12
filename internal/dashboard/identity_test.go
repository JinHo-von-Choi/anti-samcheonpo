package dashboard

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/notify"
	"testing"
)

func TestCommandRequiresMatchingLiveIdentityIncludingLegacyDaemon(t *testing.T) {
	s, _ := dashboardFixture(t)
	called := false
	s.Command = func(string, string, string) (string, error) { called = true; return "ok", nil }
	for _, id := range []string{"", "different-live-session"} {
		s.HUD = func(string) (*notify.HUDState, error) { return &notify.HUDState{SessionID: id}, nil }
		w := perform(s, "POST", "/api/command", `{"session":"fixture-session","name":"keep","arg":"now"}`, true)
		if w.Code == 200 || called {
			t.Fatal("unverified or wrong session received command")
		}
	}
}
