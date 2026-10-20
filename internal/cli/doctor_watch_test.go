package cli

import (
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hostlimit"
)

// The doctor must report the file-watch budget, because an exhausted budget
// leaves the monitor blind while it still answers "available".
func TestDoctorReportsWatchBudget(t *testing.T) {
	restore := watchBudget
	t.Cleanup(func() { watchBudget = restore })

	cases := []struct {
		name   string
		budget hostlimit.Watch
		state  string
		word   string
	}{
		{"saturated", hostlimit.Watch{Used: 128, Limit: 128, Known: true}, "error", "한도 소진"},
		{"nearly full", hostlimit.Watch{Used: 120, Limit: 128, Known: true}, "warning", "한도에 닿으면"},
		{"headroom", hostlimit.Watch{Used: 12, Limit: 128, Known: true}, "ok", "파일 감시 인스턴스"},
		{"no limit on this host", hostlimit.Watch{}, "ok", "한도를 노출하지 않는다"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			watchBudget = func() hostlimit.Watch { return c.budget }
			root := t.TempDir()
			t.Setenv("SAMCHEONPO_HOME", t.TempDir())
			report := diagnose("claude", root, "/no/samcheonpo", root+"/none.db", func(string) string { return "" }, func() bool { return false })
			var found bool
			for _, check := range report.Checks {
				if check.ID != "watch_budget" {
					continue
				}
				found = true
				if check.State != c.state {
					t.Fatalf("watch_budget state = %q, want %q (%s)", check.State, c.state, check.Message)
				}
				if !strings.Contains(check.Message, c.word) {
					t.Fatalf("watch_budget message = %q, want it to contain %q", check.Message, c.word)
				}
			}
			if !found {
				t.Fatal("doctor did not report the watch budget")
			}
		})
	}
}
