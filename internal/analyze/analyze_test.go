package analyze

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/claude"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func run(t *testing.T) *Result {
	s, err := claude.ParseFile(filepath.Join("..", "..", "testdata", "claude", "basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	prices, _ := cost.Load("")
	cfg := config.Default()
	r, err := Run(s, Options{Config: cfg, ConfigHash: config.Hash(cfg), Prices: prices})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRunInvariantAndBuckets(t *testing.T) {
	r := run(t)
	if err := CheckInvariant(r.Session, r.Totals); err != nil {
		t.Fatal(err)
	}
	if r.Totals.Micro <= 0 || r.Totals.UnpricedTokens != 0 {
		t.Errorf("totals %+v", r.Totals)
	}
	byTool := map[string]*event.Event{}
	for _, ev := range r.Session.Events {
		if ev.Bucket == "" {
			t.Errorf("event %d has no bucket", ev.Seq)
		}
		if ev.RawTool != "" {
			byTool[ev.RawTool] = ev
		}
	}
	if byTool["Edit"].Bucket != BucketProgress || byTool["Write"].Bucket != BucketProgress {
		t.Error("surviving writes are progress")
	}
	if byTool["Read"].Bucket != BucketExplore {
		t.Error("a read before production is needed exploration")
	}
	if r.Grade != GradeEstimated {
		t.Errorf("grade %s", r.Grade)
	}
	// the final message claims completion while the last verification failed
	found := false
	for _, v := range r.Verdicts {
		if v.Rule == "s5.false_done" {
			found = true
		}
	}
	if !found {
		t.Error("false completion must be detected in the fixture")
	}
}

func TestInvariantViolation(t *testing.T) {
	r := run(t)
	r.Totals.BucketMicro[BucketOther] += 1
	if err := CheckInvariant(r.Session, r.Totals); err == nil || !strings.Contains(err.Error(), "합계 불변식") {
		t.Fatalf("a broken invariant must be an error, got %v", err)
	}
}

func TestFormatFailureStopsJudgement(t *testing.T) {
	s, _ := claude.ParseFile(filepath.Join("..", "..", "testdata", "claude", "basic.jsonl"))
	s.ToolUses, s.ToolUsesPaired = 100, 50
	prices, _ := cost.Load("")
	cfg := config.Default()
	r, err := Run(s, Options{Config: cfg, Prices: prices})
	if err != nil {
		t.Fatal(err)
	}
	if r.FormatOK || len(r.Verdicts) != 0 {
		t.Fatal("format failure must stop judgement")
	}
	if err := CheckInvariant(s, r.Totals); err != nil {
		t.Fatal("costs are still summed", err)
	}
}

func TestUnpricedIsTokensOnly(t *testing.T) {
	s, _ := claude.ParseFile(filepath.Join("..", "..", "testdata", "claude", "basic.jsonl"))
	for _, ev := range s.Events {
		ev.Usage.Model = "mystery-1"
	}
	prices, _ := cost.Load("")
	cfg := config.Default()
	r, _ := Run(s, Options{Config: cfg, Prices: prices})
	if r.Totals.Micro != 0 || r.Totals.UnpricedTokens != r.Totals.Tokens || r.Totals.PriceCoverage() != 0 {
		t.Fatalf("unknown model must be counted in tokens only: %+v", r.Totals)
	}
}

func TestVirtualWorkspace(t *testing.T) {
	evs := []*event.Event{
		{Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify},
		{Kind: event.KindTool, Tool: event.ToolEdit, WriteHashes: map[string]string{"a": "1"}},
		{Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify},
		{Kind: event.KindTool, Tool: event.ToolShell, Mutating: true},
		{Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify},
	}
	for i, ev := range evs {
		ev.Seq = int64(i)
	}
	virtualWS(evs)
	if evs[0].WSBefore != evs[0].WSAfter || evs[1].WSBefore == evs[1].WSAfter {
		t.Error("only writes change the virtual workspace")
	}
	if evs[2].WSBefore != evs[1].WSAfter || evs[4].WSBefore == evs[2].WSBefore {
		t.Error("a mutating shell command changes the fingerprint")
	}
}
