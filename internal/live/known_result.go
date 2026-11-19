package live

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// Rules for a check whose result is already known.
const (
	RuleKnownResult      = "s1.known_result"
	RuleDuplicateRunning = "s1.duplicate_running"
)

// passMark is the last pass of one check: same tests, same arguments.
type passMark struct {
	seq int64
	ws  string
}

// DocFile reports a file whose change cannot change a code check's result.
// A build or test setting is not one, whatever its extension.
func DocFile(p string) bool {
	base := strings.ToLower(filepath.Base(p))
	switch filepath.Ext(base) {
	case ".md", ".markdown", ".rst", ".adoc", ".txt":
	default:
		return base == "license" || base == "notice" || base == "authors"
	}
	if strings.HasPrefix(base, "requirements") || base == "cmakelists.txt" {
		return false
	}
	return true
}

// checkKey names a check by the test commands it runs, with their
// arguments. A different option or environment is a different check; what
// the command pipes the output through, or runs before the tests, is not.
func (s *Session) checkKey(ev *event.Event) string {
	if ev.Tool != event.ToolShell || ev.Category != event.CatVerify || ev.CmdNorm == "" {
		return ""
	}
	key, tests := s.eng.CheckTests(ev)
	if len(tests) == 0 {
		return ""
	}
	return key + "\x00" + strings.Join(tests, "\x00")
}

// writesKind sorts what one call writes: nothing, documentation only, or
// something that may change a check.
func (s *Session) writesKind(ev *event.Event) string {
	paths := s.eng.WrittenPaths(ev)
	if len(paths) == 0 {
		if ev.Tool == event.ToolShell && (ev.Mutating || ev.Unknown) || ev.Category == event.CatProduce {
			// it writes, but the files are not known
			return "code"
		}
		return "unchanged"
	}
	for _, p := range paths {
		if !DocFile(p) {
			return "code"
		}
	}
	return "docs_only"
}

// notePass keeps the last pass of each check. Called with s.mu held.
func (s *Session) notePass(ev *event.Event) {
	if !detect.ExecutedVerify(ev) || *ev.ExitCode != 0 {
		return
	}
	key := s.checkKey(ev)
	if key == "" {
		return
	}
	if s.passes == nil {
		s.passes = map[string]passMark{}
	}
	s.passes[key] = passMark{seq: ev.Seq, ws: ev.WSBefore}
}

// changeSince sorts what changed after seq: nothing, documentation only,
// or something that may change a check (code, an unknown command, a file
// the session did not see written). Called with s.mu held.
func (s *Session) changeSince(seq int64, ws, now string) string {
	docs := false
	for _, ev := range s.eng.St.Events {
		if ev.Seq <= seq || ev.Kind != event.KindTool {
			continue
		}
		switch s.writesKind(ev) {
		case "code":
			return "code"
		case "docs_only":
			docs = true
		}
	}
	if docs {
		return "docs_only"
	}
	if ws != "" && now != "" && ws != now {
		// the tree changed without a write the session saw
		return "code"
	}
	return "unchanged"
}

var rerunAsk = regexp.MustCompile(`(?i)(다시|한\s*번\s*더|또)\s*(돌려|실행|시험|테스트)|rerun|run .*again|flaky|불안정`)

// knownResultPre judges a check that already passed with the same tests and
// arguments, when nothing that could change its result changed since, and a
// second copy of a check that is still running. The user asking for a rerun
// after the pass lets it through. Called with s.mu held.
func (s *Session) knownResultPre(ev *event.Event) *detect.Signal {
	key := s.checkKey(ev)
	if key == "" {
		return nil
	}
	// the earliest running copy names the finding, whatever the map order
	var running *event.Event
	for id, other := range s.awaiting {
		if id != ev.CallID && other.Seq < ev.Seq && other.ExitCode == nil && (running == nil || other.Seq < running.Seq) && s.checkKey(other) == key {
			running = other
		}
	}
	if running != nil {
		if !s.firstShadowNote(RuleDuplicateRunning, fmt.Sprint(running.Seq)) {
			return nil
		}
		return &detect.Signal{Detector: "S1", Rule: RuleDuplicateRunning, Confidence: 0.9, Level: detect.L1, Evidence: []int64{running.Seq, ev.Seq},
			Facts: map[string]any{"cmd": ev.CmdNorm, "running_seq": running.Seq, "target": key}}
	}
	p, ok := s.passes[key]
	if !ok {
		return nil
	}
	if s.lastRequestSeq > p.seq && rerunAsk.MatchString(s.lastRequestText) {
		return nil
	}
	kind := s.changeSince(p.seq, p.ws, ev.WSBefore)
	// the call itself may write before it runs the tests
	switch s.writesKind(ev) {
	case "code":
		return nil
	case "docs_only":
		if kind == "unchanged" {
			kind = "docs_only"
		}
	}
	if kind == "code" || !s.firstShadowNote(RuleKnownResult, fmt.Sprint(p.seq)+kind) {
		return nil
	}
	return &detect.Signal{Detector: "S1", Rule: RuleKnownResult, Confidence: 0.9, Level: detect.L1, Evidence: []int64{p.seq, ev.Seq},
		Facts: map[string]any{"cmd": ev.CmdNorm, "kind": kind, "passed_seq": p.seq, "target": key}}
}

// firstShadowNote reports whether a rule that is only recorded should record
// this mark. A recorded verdict is stored after the answer under the session
// lock; calls that repeat the same finding many times (a host that never
// reports results) would queue a ledger write each and hold the next hooks.
// A rule that can refuse is judged every time. Called with s.mu held.
func (s *Session) firstShadowNote(rule, mark string) bool {
	if !slices.Contains(s.Cfg.Rollout.ShadowRules, rule) && s.Cfg.Rollout.Mode != "shadow" {
		return true
	}
	if s.shadowNoted == nil {
		s.shadowNoted = map[string]bool{}
	}
	k := rule + "\x00" + mark
	if s.shadowNoted[k] {
		return false
	}
	s.shadowNoted[k] = true
	return true
}

// docsOnlySincePass reports whether everything changed since the last
// passing check is documentation. Called with s.mu held.
func (s *Session) docsOnlySincePass(ev *event.Event) bool {
	key := s.checkKey(ev)
	p, ok := s.passes[key]
	if !ok {
		var last int64 = -1
		for _, m := range s.passes {
			if m.seq > last {
				last, p = m.seq, m
			}
		}
		if last < 0 {
			return false
		}
	}
	return s.changeSince(p.seq, p.ws, ev.WSBefore) == "docs_only"
}
