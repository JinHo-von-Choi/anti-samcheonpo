package cli

import (
	"fmt"
	"os"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
)

func runIDRepl(ev *event.Event) string {
	if ev.ExecFP != "" {
		return ev.ExecFP
	}
	return ev.CmdFP
}

func maxLen(st []int64) int {
	m := 0
	for i := 1; i <= len(st); i++ {
		_ = i
		m = len(st)
	}
	return m
}

func llist(st []int64) []int64 { return st }

// Temp probe (never runs in CI): parses a raw harness transcript through the
// same analyze pipeline the receipt uses and counts s1.verify_streak signals
// plus per-rule volume. Set SAMCHEONPO_PROBE_FILE, SAMCHEONPO_PROBE_AGENT
// (claude|codex) and SAMCHEONPO_PROBE_PROJECT to run.
func TestTempTranscriptProbe(t *testing.T) {
	path := os.Getenv("SAMCHEONPO_PROBE_FILE")
	agent := os.Getenv("SAMCHEONPO_PROBE_AGENT")
	if path == "" || agent == "" {
		t.Skip("probe: set SAMCHEONPO_PROBE_FILE and SAMCHEONPO_PROBE_AGENT")
	}
	s, err := sources.Parse(sources.File{Path: path, Agent: agent})
	if err != nil {
		t.Fatal(err)
	}
	prices, err := cost.Load("")
	if err != nil {
		t.Fatal(err)
	}
	res, err := analyze.Run(s, analyze.Options{Config: config.Default(), Prices: prices})
	if err != nil {
		t.Fatal(err)
	}
	perRule := map[string]int{}
	var streakSeqs []int64
	for i := range res.Verdicts {
		perRule[res.Verdicts[i].Rule]++
		if res.Verdicts[i].Rule == "s1.verify_streak" {
			streakSeqs = append(streakSeqs, res.Verdicts[i].Seq)
		}
	}
	fmt.Printf("file=%s events=%d verdicts=%d project=%s\n", path, len(s.Events), len(res.Verdicts), s.ProjectPath)
	for r, n := range perRule {
		fmt.Printf("  %-28s %d\n", r, n)
	}
	fmt.Printf("  s1.verify_streak seqs: %v\n", streakSeqs)

	// replica streak with per-event tracing
	writes := 0
	lastWriteSeq := int64(-1)
	var st []int64
	var lastVerify *event.Event
	verbose := os.Getenv("SAMCHEONPO_PROBE_TRACE") == "1"
	for i := range s.Events {
		ev := s.Events[i]
		if ev.Kind == event.KindTool && len(ev.Paths) > 0 && (ev.Tool == event.ToolWrite || ev.Tool == event.ToolEdit || ev.ShellClass == classify.ShellProduce) {
			writes++
			lastWriteSeq = ev.Seq
		}
		if !detect.ExecutedVerify(ev) {
			continue
		}
		prev := lastVerify
		lastVerify = ev
		prog := false
		if prev != nil && prev.ResultFP == ev.ResultFP {
			// (none)
		}
		if prev != nil && runIDRepl(prev) == runIDRepl(ev) && prev.ResultFP != ev.ResultFP {
			pf, cf := *prev.ExitCode > 0, *ev.ExitCode > 0
			if pf && !cf {
				prog = true
			}
		} else if *ev.ExitCode == 0 && writes > 0 && (prev == nil || prev.Seq < lastWriteSeq) {
			prog = true
		}
		if *ev.ExitCode > 0 {
			st = nil
		} else if prev != nil && runIDRepl(prev) == runIDRepl(ev) {
			// skip
		} else if prog {
			st = nil
		} else {
			st = append(st, ev.Seq)
		}
		if verbose {
			fmt.Printf("  vstreak seq=%d len=%d prog=%v runid=%s ex=%d\n", ev.Seq, len(st), prog, runIDRepl(ev)[:8], *ev.ExitCode)
		}
	}
	fmt.Printf("  replica streak max=%d replay=%v\n", maxLen(st), llist(st))
}
