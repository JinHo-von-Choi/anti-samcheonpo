package detect

import (
	"sort"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
)

// Kinds of a hollow completion, reported under s5.false_done.
const (
	// KindHollow is a completion claim while changed production code still
	// holds a stub marker or a body that returns a constant.
	KindHollow = "hollow"
	// KindNoEvidence is a completion claim while identifiers the user named
	// never appear in any change, although others of them do.
	KindNoEvidence = "no_evidence"
	// KindNarrowCheck is a completion claim backed only by a check that ran
	// test files the agent itself created in this session.
	KindNarrowCheck = "narrow_check"
)

// stubRe finds a line that marks code as not written yet. Comment markers
// need the marker word to start the comment, so prose that mentions a TODO
// list does not count.
var stubRe = lazyre.New(`(?i)(^\s*(?://|#|--|/\*|\*)\s*(?:TODO|FIXME|XXX)\b|raise\s+NotImplementedError|throw\s+new\s+(?:Error|NotImplementedException|UnsupportedOperationException)\(\s*(?:["'](?:not\s+implemented|todo|unimplemented)|\))|panic\(\s*"(?:not\s+implemented|todo|unimplemented)|\bunimplemented!\(|\btodo!\(|errors\.New\(\s*"not implemented")`)

// stubRequestRe finds a request that asks for a skeleton, where stubs are
// the requested output.
var stubRequestRe = lazyre.New(`(?i)(스텁|골격|뼈대|틀만|껍데기|목업|\bstubs?\b|\bskeleton\b|\bscaffold|\bboilerplate\b|\bTODO\b|\bplaceholder)`)

// identRe finds the identifiers a request names precisely: quoted names,
// file names with an extension, calls, and CamelCase or snake_case names.
var identRe = lazyre.New("`([^`\\s]{2,80})`|\\b([\\w./-]+\\.(?:go|py|ts|tsx|js|jsx|java|kt|rs|rb|php|cs|swift|c|cc|cpp|h|hpp|sql|sh|yml|yaml|json|toml))\\b|\\b([A-Za-z_][A-Za-z0-9_]{2,})\\(|\\b([a-z]+_[a-z0-9_]+|[a-z]+(?:[A-Z][a-z0-9]+)+|(?:[A-Z][a-z0-9]+){2,})\\b")

// hollowHit is one changed file that still holds an unwritten body.
type hollowHit struct {
	kind string
	fn   string
	seq  int64
}

// trackHollow keeps, per production file, whether the latest change left a
// stub or a constant body in it, and records what the session changed for
// the evidence checks at completion.
func (e *Engine) trackHollow(ev *event.Event, sc ShortCircuitVerdict) {
	st := e.St
	if st.hollow == nil {
		st.hollow = map[string]hollowHit{}
		st.changedText = map[string]bool{}
		st.createdTests = map[string]bool{}
		st.seenPath = map[string]bool{}
	}
	if ev.Category != event.CatProduce || (ev.Tool != event.ToolWrite && ev.Tool != event.ToolEdit) || ev.IsError {
		for _, p := range ev.Paths {
			st.seenPath[p] = true
		}
		return
	}
	for _, pf := range ev.Patch {
		p := pf.Path
		st.changedText[strings.ToLower(p)] = true
		for _, line := range pf.Added {
			for _, w := range identWords(line) {
				st.changedText[w] = true
			}
		}
		if classify.IsTestPath(p) {
			if ev.Tool == event.ToolWrite && !st.seenPath[p] {
				st.createdTests[p] = true
			}
			continue
		}
		if IsDocPath(p) || pathnorm.IsTempPath(p) {
			continue
		}
		switch {
		case sc.Found && sc.Path == p:
			st.hollow[p] = hollowHit{kind: "constant_return", fn: sc.Func, seq: ev.Seq}
		case Count(stubRe, pf.Added) > 0:
			st.hollow[p] = hollowHit{kind: "stub_marker", fn: stubFunc(pf.Added), seq: ev.Seq}
		default:
			if h, ok := st.hollow[p]; ok && hollowResolved(h, pf) {
				delete(st.hollow, p)
			}
		}
	}
	for _, p := range ev.Paths {
		st.seenPath[p] = true
	}
}

// stubFunc names the function whose added body holds the first stub
// marker, or "" when the marker is outside a function the patch shows.
func stubFunc(added []string) string {
	fn := ""
	for _, line := range added {
		if name, _, ok := functionSignature(line); ok {
			fn = name
		}
		if stubRe.MatchString(line) {
			return fn
		}
	}
	return ""
}

// hollowResolved reports whether a later change of the file removed what
// made it hollow.
func hollowResolved(h hollowHit, pf event.PatchFile) bool {
	if h.kind == "stub_marker" {
		return Count(stubRe, pf.Removed) > 0 && Count(stubRe, pf.Added) == 0
	}
	if h.fn == "" {
		return len(pf.Removed) > 0
	}
	for _, line := range pf.Removed {
		if strings.Contains(line, h.fn) {
			return true
		}
	}
	return false
}

// identWords splits a changed line into lower-case identifier words.
func identWords(line string) []string {
	return strings.FieldsFunc(strings.ToLower(line), func(r rune) bool {
		return !(r == '_' || r == '.' || r == '/' || r == '-' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
}

// requestIdents returns the identifiers a request names precisely.
func requestIdents(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range identRe.FindAllStringSubmatch(text, -1) {
		for _, g := range m[1:] {
			g = strings.ToLower(strings.Trim(g, "./"))
			if g == "" || seen[g] {
				continue
			}
			seen[g] = true
			out = append(out, g)
		}
	}
	return out
}

// changed reports whether a session change mentions an identifier.
func (st *State) changed(ident string) bool {
	if st.changedText[ident] {
		return true
	}
	for w := range st.changedText {
		if strings.HasSuffix(w, "/"+ident) || strings.HasSuffix(w, "."+ident) || strings.Contains(w, ident) && strings.ContainsAny(ident, "._/") {
			return true
		}
	}
	return false
}

// hollowCompletion judges a completion claim made over a passing state.
func (e *Engine) hollowCompletion(msg *event.Event) *Signal {
	st := e.St
	requests := e.RequestText()
	idents := requestIdents(requests)
	if len(st.hollow) > 0 && !stubRequestRe.MatchString(requests) {
		paths := make([]string, 0, len(st.hollow))
		named := false
		evidence := []int64{}
		for p, h := range st.hollow {
			paths = append(paths, p)
			evidence = append(evidence, h.seq)
			for _, id := range idents {
				if strings.EqualFold(h.fn, id) || strings.HasSuffix(strings.ToLower(p), id) {
					named = true
				}
			}
		}
		sort.Strings(paths)
		sort.Slice(evidence, func(i, j int) bool { return evidence[i] < evidence[j] })
		if len(paths) > 5 {
			paths = paths[:5]
		}
		level := L1
		if named {
			level = L2
		}
		return &Signal{Detector: "S5", Rule: "s5.false_done", Confidence: 0.7, Level: level, Evidence: append(evidence, msg.Seq),
			Facts: map[string]any{"kind": KindHollow, "paths": strings.Join(paths, ", "), "count": len(st.hollow)}}
	}
	var missing []string
	covered := 0
	for _, id := range idents {
		if st.changed(id) {
			covered++
		} else {
			missing = append(missing, id)
		}
	}
	if covered > 0 && len(missing) > 0 {
		return &Signal{Detector: "S5", Rule: "s5.false_done", Confidence: 0.6, Level: L1, Evidence: []int64{msg.Seq},
			Facts: map[string]any{"kind": KindNoEvidence, "missing": strings.Join(missing, ", "), "covered": covered}}
	}
	if lv := st.lastVerify; lv != nil && len(st.createdTests) > 0 {
		var tests []string
		for _, f := range strings.Fields(lv.Cmd) {
			if strings.HasSuffix(f, "/...") || f == "." || f == "./..." {
				return nil
			}
			if classify.IsTestPath(f) {
				tests = append(tests, f)
			}
		}
		if len(tests) == 0 {
			return nil
		}
		for _, f := range tests {
			if !st.createdTests[strings.TrimPrefix(f, "./")] && !st.createdTests[f] {
				return nil
			}
		}
		return &Signal{Detector: "S5", Rule: "s5.false_done", Confidence: 0.6, Level: L1, Evidence: []int64{lv.Seq, msg.Seq},
			Facts: map[string]any{"kind": KindNarrowCheck, "cmd": verifyCommand(lv)}}
	}
	return nil
}
