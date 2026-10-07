package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
)

func TestInterventionsCountsStagesPerRule(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	d, err := ledger.Open(filepath.Join(home, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	mk := func(id, rule string, to ...recovery.Stage) {
		a := recovery.Attempt{Version: "recovery/1", ID: id, Agent: "claude", SessionID: "s-" + id, Revision: 1, CauseKey: id, VerdictID: "v" + id, Rule: rule, CreatedAt: now, Stage: recovery.Proposed, Prescription: recovery.For(recovery.Code), Route: "advice"}
		if err := d.SaveRecovery(a); err != nil {
			t.Fatal(err)
		}
		for _, st := range to {
			if st == recovery.EffectObserved {
				a.Observation = "no_recurrence_observed"
			}
			if err := a.Advance(st, now); err != nil {
				t.Fatal(err)
			}
			if err := d.SaveRecovery(a); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("1", "s2.stuck_error", recovery.Emitted, recovery.Delivered, recovery.EffectObserved)
	mk("2", "s2.stuck_error", recovery.Censored)
	mk("3", "s1.identical_rerun")
	d.Close()
	cmd := interventionsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--since", "1d"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"s2.stuck_error", "s1.identical_rerun", "인과 효과가 아니다"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "s2.stuck_error") && strings.Join(strings.Fields(line), " ") != "s2.stuck_error 2 1 1 0 1 0 1" {
			t.Fatalf("per-stage counts: %q", line)
		}
	}
}
