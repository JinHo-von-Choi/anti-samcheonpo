package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
)

func accuracySeed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	d, err := ledger.Open(filepath.Join(home, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	// One effect-observed prescription: no recurrence in the window.
	a := recovery.Attempt{Version: "recovery/1", ID: "1", Agent: "claude", SessionID: "s1", Revision: 1, CauseKey: "c1", VerdictID: "v1", Rule: "s1.identical_rerun", CreatedAt: time.Now(), Stage: recovery.Proposed, Prescription: recovery.For(recovery.Code), Route: "advice"}
	if err := d.SaveRecovery(a); err != nil {
		t.Fatal(err)
	}
	a.Observation = "no_recurrence_observed"
	for _, st := range []recovery.Stage{recovery.Emitted, recovery.Delivered, recovery.EffectObserved} {
		if err := a.Advance(st, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := d.SaveRecovery(a); err != nil {
			t.Fatal(err)
		}
	}
	// One censored prescription on the same rule.
	b := recovery.Attempt{Version: "recovery/1", ID: "2", Agent: "claude", SessionID: "s1", Revision: 2, CauseKey: "c2", VerdictID: "v2", Rule: "s1.identical_rerun", CreatedAt: time.Now(), Stage: recovery.Proposed, Prescription: recovery.For(recovery.Code), Route: "advice"}
	if err := d.SaveRecovery(b); err != nil {
		t.Fatal(err)
	}
	if err := b.Advance(recovery.Censored, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveRecovery(b); err != nil {
		t.Fatal(err)
	}
	// Two blocks of another rule on the agent channel.
	for i := 0; i < 2; i++ {
		if _, err := d.Exec(`INSERT INTO intervention(verdict_id,session_id,seq,channel,level,blocked,rule,delivery_state) VALUES('v3','s2',?,'agent',2,1,'s2.stuck_error','proposed')`, i); err != nil {
			t.Fatal(err)
		}
	}
	// A session row so blocks join to a recorded session.
	if err := d.UpsertLive("s2", "claude", t.TempDir(), "", ""); err != nil {
		t.Fatal(err)
	}
	// Users' own judgments of both rules.
	if err := d.AddFeedback("v4", "s3", "s1.identical_rerun", "false_positive", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := d.AddFeedback("v5", "s3", "s2.stuck_error", "extend", "", "", ""); err != nil {
		t.Fatal(err)
	}
	d.Close()
}

func TestAccuracyCombinesBlocksPrescriptionsAndFeedback(t *testing.T) {
	accuracySeed(t)
	cmd := accuracyCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--since", "30d"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"s1.identical_rerun", "s2.stuck_error", "합계", "위양성"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "s1.identical_rerun") {
			fields := strings.Fields(line)
			if len(fields) != 12 {
				t.Fatalf("columns: %q", line)
			}
			// 차단 제안 전달 인지 재발없음 재발 미관측 위양성 확인 정밀도 판정정밀도
			if fields[1] != "0" {
				t.Fatalf("blocked count = %q, want 0 (rule never blocked)", fields[1])
			}
			if fields[2] != "2" {
				t.Fatalf("proposed count = %q, want 2 from two attempts", fields[2])
			}
			if fields[3] != "1" || fields[5] != "1" || fields[6] != "0" {
				t.Fatalf("no-recurrence columns (전달 재발없음 재발) = %q", line)
			}
			if fields[7] != "1" {
				t.Fatalf("censored count = %q", fields[7])
			}
			if fields[8] != "1" {
				t.Fatalf("false positive feedback = %q", fields[8])
			}
			if fields[9] != "0" {
				t.Fatalf("confirmed feedback = %q", fields[9])
			}
			if fields[10] != "100.0" {
				t.Fatalf("precision estimate = %q, want 100.0", fields[10])
			}
			if fields[11] != "0.0" {
				t.Fatalf("judged precision = %q, want 0.0 from one false positive", fields[11])
			}
			return
		}
	}
}

func TestAccuracyJSONParses(t *testing.T) {
	accuracySeed(t)
	cmd := accuracyCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--since", "30d", "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got struct {
		GeneratedAt string             `json:"generated_at"`
		Rules       []accuracyRuleJSON `json:"rules"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("bad json %q", out.String())
	}
	if len(got.Rules) != 2 {
		t.Fatalf("rule count = %d, want 2", len(got.Rules))
	}
}
