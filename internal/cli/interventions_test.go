package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
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
		// 제안 발신 전달 인지 차단 재발없음 재발 미관측
		if strings.HasPrefix(line, "s2.stuck_error") && strings.Join(strings.Fields(line), " ") != "s2.stuck_error 2 1 1 0 0 1 0 1" {
			t.Fatalf("per-stage counts: %q", line)
		}
	}
}

// A block is an action, not a prescription. It must appear in the report,
// because a session that advised once and blocked three times is not a
// session with one intervention.
func TestInterventionsCountsBlocksSeparately(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	d, err := ledger.Open(filepath.Join(home, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	a := recovery.Attempt{Version: "recovery/1", ID: "1", Agent: "claude", SessionID: "s1", Revision: 1, CauseKey: "c", VerdictID: "v1", Rule: "s1.identical_rerun", CreatedAt: now, Stage: recovery.Proposed, Prescription: recovery.For(recovery.Code), Route: "advice"}
	if err := d.SaveRecovery(a); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO verdict(id,session_id,seq,detector,rule,confidence,evidence_event_ids,waste_micro_krw,level) VALUES(?,?,?,?,?,?,?,?,?)`,
		"v1", "s1", 1, "S1", "s1.identical_rerun", 0.9, "[]", 0, int(detect.L2)); err != nil {
		t.Fatal(err)
	} // Three blocks on the agent channel: the tool call was stopped.
	for i := 0; i < 3; i++ {
		if err := d.RecordIntervention(detect.Signal{ID: "v1", Rule: "s1.identical_rerun", Level: detect.L2}, "s1", "agent", true); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.UpsertLive("s1", "claude", t.TempDir(), "", ""); err != nil {
		t.Fatal(err)
	}
	d.Close()
	cmd := interventionsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--since", "1d"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "s1.identical_rerun") {
			fields := strings.Fields(line)
			// 제안 발신 전달 인지 차단 재발없음 재발 미관측
			if len(fields) != 9 {
				t.Fatalf("unexpected columns: %q", line)
			}
			if fields[5] != "3" {
				t.Fatalf("blocked count = %q, want 3: %q", fields[5], line)
			}
			if fields[1] != "1" {
				t.Fatalf("prescription count = %q, want 1: %q", fields[1], line)
			}
			return
		}
	}
	t.Fatalf("rule row missing:\n%s", text)
}

func TestInterventionsAgentFilterAndUnknownReason(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "hermes"} {
		a := recovery.Attempt{Version: "recovery/1", ID: agent, Agent: agent, SessionID: agent, CauseKey: agent, VerdictID: agent, Rule: "s2.stuck_error", CreatedAt: time.Now(), Stage: recovery.Proposed, Route: "observe", Observation: "unsupported_delivery"}
		if err = db.SaveRecovery(a); err != nil {
			t.Fatal(err)
		}
		if err = a.Advance(recovery.Censored, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err = db.SaveRecovery(a); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	cmd := interventionsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--since", "all", "--agent", "hermes", "--by-agent"})
	if err = cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "hermes/s2.stuck_error") || !strings.Contains(out.String(), "hermes/unsupported_delivery: 1") || strings.Contains(out.String(), "claude/") {
		t.Fatalf("%s", out.String())
	}
}
