package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestReportUnknownPriceIsNotFreeOrZeroWaste(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.UpsertLive("unknown", "hermes", t.TempDir(), "", "unknown-model"); err != nil {
		t.Fatal(err)
	}
	ev := &event.Event{Seq: 1, TS: time.Now(), Kind: event.KindTool, Tool: event.ToolShell, Usage: event.Usage{In: 100}, Priced: false}
	if err = db.InsertLiveEvent("unknown", ev); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE event SET bucket='waste',symptom='S2' WHERE session_id='unknown'`); err != nil {
		t.Fatal(err)
	}
	cmd := reportCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--since", "all"})
	if err = cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"단가 미확인", "토큰", "100.0%", "live", "완료 미확인"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "0원") {
		t.Fatalf("unknown price shown as free: %s", out.String())
	}
}

func TestReportUnobservedUsageAndModeFilter(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	if err = db.UpsertLive("empty", "opencode", t.TempDir(), "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO session(id,agent,mode,started_at) VALUES('old','claude','audit',?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	cmd := reportCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--since", "all", "--mode", "live"})
	if err = cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "사용량 미확인") {
		t.Fatalf("%s", out.String())
	}
	if strings.Contains(out.String(), "0.0%") || strings.Contains(out.String(), "\nclaude audit ") {
		t.Fatalf("zero observation or mixed mode: %s", out.String())
	}
}

func TestReportPriceBasisAndLegacyMetadata(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"full", "partial"} {
		if err = db.UpsertLive(id, id, t.TempDir(), "", ""); err != nil {
			t.Fatal(err)
		}
		ev := &event.Event{Seq: 1, TS: time.Now(), Kind: event.KindTool, Usage: event.Usage{In: 100}, CostMicroKRW: 2000000, Priced: true}
		if err = db.InsertLiveEvent(id, ev); err != nil {
			t.Fatal(err)
		}
		if id == "partial" {
			ev.Seq = 2
			ev.Priced = false
			ev.CostMicroKRW = 0
			if err = db.InsertLiveEvent(id, ev); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = db.Exec(`UPDATE event SET bucket='waste',symptom='S2'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM session_observation WHERE session_id='full'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	c := reportCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetArgs([]string{"--since", "all"})
	if err = c.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"full live 1 2원 원화 100.0%", "partial live 1 2원(확인된 일부 환산액) 토큰 100.0%", "50.0%", "기존 기록·종료 확인 불가 / 판정기 버전 미확인"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q:\n%s", want, out.String())
		}
	}
}
