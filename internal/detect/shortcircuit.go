package detect

import (
	"regexp"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// The short circuit detector finds a function that returns a constant
// without ever reading its parameters. That is the shape of a test made to
// pass by construction: the call still runs, the assertion still sees a
// value, and nothing about the behaviour was checked.
//
// The rule starts in shadow. A pattern like this also appears in legitimate
// code (a getter that returns a constant, a stub), so the finding is
// recorded and reported until a person confirms it.

var (
	// goFunc: func Name(params) ... {
	goFuncRE = regexp.MustCompile(`^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*\(([^)]*)\)`)
	// pyFunc: def name(params): or async def name(params):
	pyFuncRE = regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(([^)]*)\)\s*:`)
	// jsFunc: function name(params) { or name(params) {
	jsFuncRE = regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$]\w*)\s*\(([^)]*)\)`)
	// method: name(params) { in a class body
	methodRE = regexp.MustCompile(`^\s{2,}(?:async\s+)?([A-Za-z_$]\w*)\s*\(([^)]*)\)\s*\{`)
	// returnLiteral: return True / return true / return 1 / return null
	returnLiteralRE = regexp.MustCompile(`^\s*return\s+(true|false|True|False|None|null|nil|1|0)\s*[;,]?\s*$`)
)

// ShortCircuitVerdict is the finding of the short circuit detector.
type ShortCircuitVerdict struct {
	Found  bool
	Path   string
	Func   string
	Reason string
}

// ShortCircuit inspects added lines of business code.
type ShortCircuit struct{}

// NewShortCircuit returns a detector.
func NewShortCircuit() *ShortCircuit { return &ShortCircuit{} }

// InspectAdded judges the added lines of one file.
//
// The check is deliberately narrow: a function whose parameters are never
// mentioned and whose body returns a constant. A function that mentions a
// parameter, returns something else, or has more than a constant in its
// body is not a finding.
func (s *ShortCircuit) InspectAdded(path string, added []string) ShortCircuitVerdict {
	for i, line := range added {
		name, params, ok := functionSignature(line)
		if !ok || len(params) == 0 {
			continue
		}
		body := added[i+1:]
		limit := len(body)
		for j, l := range body {
			if j > 0 {
				if _, _, again := functionSignature(l); again {
					limit = j
					break
				}
			}
		}
		body = body[:limit]
		if len(body) == 0 {
			continue
		}
		if !returnLiteralRE.MatchString(body[0]) {
			continue
		}
		mentioned := false
		for _, p := range params {
			if p == "" || p == "_" {
				continue
			}
			for _, l := range body {
				if mentionsIdentifier(l, p) {
					mentioned = true
					break
				}
			}
			if mentioned {
				break
			}
		}
		if mentioned {
			continue
		}
		return ShortCircuitVerdict{
			Found:  true,
			Path:   path,
			Func:   name,
			Reason: "returns a constant without reading its parameters",
		}
	}
	return ShortCircuitVerdict{}
}

// functionSignature reads a function definition and its parameter names.
func functionSignature(line string) (string, []string, bool) {
	for _, re := range []*regexp.Regexp{goFuncRE, pyFuncRE, jsFuncRE, methodRE} {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		params := splitParams(m[2])
		if len(params) == 0 {
			return name, nil, false
		}
		return name, params, true
	}
	return "", nil, false
}

// splitParams reads parameter names out of a parameter list.
func splitParams(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		// drop a type annotation, a default value and a modifier
		if i := strings.IndexAny(p, "=:"); i >= 0 {
			p = p[:i]
		}
		p = strings.TrimSpace(p)
		if i := strings.LastIndexAny(p, " \t*"); i >= 0 {
			p = p[i+1:]
		}
		p = strings.TrimPrefix(p, "**")
		p = strings.TrimPrefix(p, "*")
		p = strings.TrimPrefix(p, "&")
		if p == "" || p == "self" || p == "cls" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// mentionsIdentifier reports whether the line uses the name as a word.
func mentionsIdentifier(line, name string) bool {
	for _, part := range strings.FieldsFunc(line, func(r rune) bool {
		return !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}) {
		if part == name {
			return true
		}
	}
	return false
}

// recordShortCircuit notes a new function that returns a constant without
// reading its parameters.
//
// The finding is recorded, not delivered: the rule starts in shadow because
// the same shape appears in legitimate code. A person decides whether it
// was a fake pass.
func (e *Engine) recordShortCircuit(ev *event.Event, sigs *[]Signal) {
	if ev.Category != event.CatProduce {
		return
	}
	if ev.Tool != event.ToolWrite && ev.Tool != event.ToolEdit {
		return
	}
	for _, pf := range ev.Patch {
		if classify.IsTestPath(pf.Path) {
			continue
		}
		if v := e.shortCircuit.InspectAdded(pf.Path, pf.Added); v.Found {
			e.add(sigs, Signal{Detector: "S5", Rule: "s5.short_circuit", Confidence: 0.6, Level: L1,
				Evidence: []int64{ev.Seq},
				Facts:    map[string]any{"path": v.Path, "func": v.Func, "reason": v.Reason, "recorded": true}})
			return
		}
	}
}
