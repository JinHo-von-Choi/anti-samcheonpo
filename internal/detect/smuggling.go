package detect

import (
	"sort"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// The rules and kinds of the smuggling scan.
//
// The threat this covers is not the agent deceiving itself. It is the agent
// being told to deceive: a prompt, a file, a tool result or a memory file
// carrying an instruction aimed at the monitor or at the report. The scan
// runs on the assembled context, not on the user turn alone, because an
// instruction that arrives inside a file is still an instruction.
//
// The scan is deterministic and literal-based. It names the shape of what
// it found and never quotes the text it found it in.
const (
	// RuleSmuggling is an instruction aimed at the monitor or the report.
	RuleSmuggling = "s9.instruction_smuggling"
	// RuleFakeResult is an instruction to present an unperformed check as
	// performed.
	RuleFakeResult = "s9.unverified_claim_request"
)

// Smuggling kinds. The kind is what the escalation key is built from, so
// it has to name the shape of the instruction and nothing else.
const (
	// KindBypassMonitor asks the agent to stop reporting, or to stop the
	// monitor itself.
	KindBypassMonitor = "bypass_monitor"
	// KindHideReport asks the agent to keep something from the user.
	KindHideReport = "hide_report"
	// KindFakeResult asks the agent to report a check it did not run.
	KindFakeResult = "fake_result"
)

// smugglingWindow is how far apart the act and its target may be and still
// be one instruction. Two unrelated sentences in one file are not one
// instruction.
const smugglingWindow = 140

// maxSmugglingText bounds how much of one text is scanned.
const maxSmugglingText = 8000

// maxSmugglingHits bounds how many hits one session keeps.
const maxSmugglingHits = 40

// smugglingTerm is one literal of an act or a target.
//
// The scan searches for literals instead of running patterns. A pattern
// with many alternatives costs time proportional to the text times the
// alternatives, and this scan runs on every observed event that carries
// text. A literal search is a straight byte scan.
//
// English terms carry word boundaries. Without them "applies" contains
// "lie" and "latest" contains "test", and ordinary output would read as an
// instruction.
type smugglingTerm struct {
	text  string
	ascii bool
}

// smugglingPattern is one instruction shape: an act and what the act is
// aimed at. Both have to appear in the same window.
type smugglingPattern struct {
	kind    string
	acts    []smugglingTerm
	targets []smugglingTerm
}

// kr makes a Korean term. Korean is written without spaces, so the term
// stands on its own and needs no boundary check.
func kr(texts ...string) []smugglingTerm {
	out := make([]smugglingTerm, 0, len(texts))
	for _, t := range texts {
		out = append(out, smugglingTerm{text: t})
	}
	return out
}

// en makes English terms. Each one has to be a whole word.
func en(texts ...string) []smugglingTerm {
	out := make([]smugglingTerm, 0, len(texts))
	for _, t := range texts {
		out = append(out, smugglingTerm{text: t, ascii: true})
	}
	return out
}

// The acts that take the monitor off the agent.
var (
	// actMonitor covers ignoring, hiding, disabling and going around.
	actMonitor = append(kr("무시", "숨기", "숨겨", "숨김", "말하지 마", "말하지마", "알리지 마", "알리지마",
		"보고하지 마", "보고하지마", "기록하지 마", "기록하지마", "비공개", "속여", "기만", "우회", "끄고", "끄러",
		"비활성화", "제거하고", "삭제하고"),
		en("ignore", "ignores", "ignored", "ignoring", "hide", "hides", "hiding", "hidden",
			"conceal", "conceals", "concealed", "concealing", "omit", "omits", "omitted", "omitting",
			"suppress", "suppresses", "suppressed", "suppressing", "bypass", "bypasses", "bypassed",
			"bypassing", "deceive", "deceives", "deceived", "deceiving", "mislead", "misleads",
			"misled", "misleading", "without telling", "without reporting", "without mentioning",
			"do not tell", "do not report", "do not mention", "do not log", "don't tell", "don't report",
			"don't mention", "don't log", "turn off", "disable", "disables", "disabled", "uninstall")...)
	// actReport covers keeping something from the person who reads the
	// report. It leaves out "ignore", which is a scope problem rather than
	// a deception.
	actReport = append(kr("숨기", "숨겨", "숨김", "말하지 마", "말하지마", "알리지 마", "알리지마",
		"보고하지 마", "보고하지마", "비공개"),
		en("hide", "hides", "hiding", "hidden", "conceal", "conceals", "concealed", "concealing",
			"omit", "omits", "omitted", "omitting", "without telling", "without reporting",
			"without mentioning", "do not tell", "do not report", "do not mention", "don't tell",
			"don't report", "don't mention")...)
	// actFake covers presenting something that did not happen as if it
	// had.
	actFake = append(kr("인척", "인 척", "인것처럼", "인 것처럼", "처럼말", "처럼 말", "말아주세요",
		"말아 주세요", "말아라"),
		en("pretend", "pretends", "act like", "act as if", "fake it", "claim", "claimed", "claims",
			"lie", "lies")...)
)

// The targets the acts are aimed at.
var (
	targetMonitor = append(kr("삼천포", "samcheonpo", "감시자", "감시", "모니터", "훅", "후크", "보고", "기록", "알림"),
		en("samcheonpo", "monitor", "monitors", "monitoring", "hook", "hooks", "report", "reports",
			"reported", "log", "logs", "logged", "notification", "notifications")...)
	targetUser  = append(kr("사용자"), en("user", "users")...)
	targetCheck = append(kr("테스트", "검사", "검증", "빌드", "통과", "성공", "실행"),
		en("test", "tests", "verify", "verifies", "verified", "build", "builds", "pass", "passes",
			"passed", "success", "successful", "run", "runs")...)
)

// smugglingPatterns are the shapes the scan knows.
var smugglingPatterns = []smugglingPattern{
	{KindBypassMonitor, actMonitor, targetMonitor},
	{KindHideReport, actReport, targetUser},
	{KindFakeResult, actFake, targetCheck},
}

// maxActPositions bounds how many act positions one pattern looks at.
//
// A file that repeats the same word a hundred times must not make the
// scan unbounded. The first positions are the ones a reader meets first.
const maxActPositions = 32

// SmugglingHit is one deception instruction found in the assembled
// context.
type SmugglingHit struct {
	// Seq is the event the instruction arrived on.
	Seq int64 `json:"seq"`
	// Rule is the rule that matched.
	Rule string `json:"rule"`
	// Kind names the shape of the instruction.
	Kind string `json:"kind"`
	// Source is where the instruction came from: prompt, message or
	// tool_output.
	Source string `json:"source"`
	// Written marks an instruction the agent itself wrote into a file.
	Written bool `json:"written,omitempty"`
}

// ruleForKind maps a kind to the rule that reports it.
func ruleForKind(kind string) string {
	if kind == KindFakeResult {
		return RuleFakeResult
	}
	return RuleSmuggling
}

// ScanSmuggling looks for a deception instruction in one text.
//
// It returns the kind of the first instruction it finds. The text is not
// returned and never stored: only the shape of the instruction is.
func ScanSmuggling(text string) (kind string, ok bool) {
	if text == "" {
		return "", false
	}
	r := []rune(text)
	if len(r) > maxSmugglingText {
		r = r[:maxSmugglingText]
	}
	lower := strings.ToLower(string(r))
	for _, p := range smugglingPatterns {
		if findInstruction(lower, p) {
			return p.kind, true
		}
	}
	return "", false
}

// findInstruction reports whether one pattern's act and target stand in the
// same window.
//
// The act decides where the window is. Two unrelated sentences in one file
// are not one instruction, so the target has to be near the act.
func findInstruction(lower string, p smugglingPattern) bool {
	positions := findTerms(lower, p.acts, maxActPositions)
	for _, pos := range positions {
		start := pos - smugglingWindow
		if start < 0 {
			start = 0
		}
		end := pos + smugglingWindow
		if end > len(lower) {
			end = len(lower)
		}
		if hasTerm(lower[start:end], p.targets, start) {
			return true
		}
	}
	return false
}

// findTerms returns the positions where the terms occur.
//
// The positions are the starts of the matches, in order, up to the limit.
// A term that is part of a longer word does not count.
func findTerms(lower string, terms []smugglingTerm, limit int) []int {
	var out []int
	for _, t := range terms {
		from := 0
		for from <= len(lower) {
			i := strings.Index(lower[from:], t.text)
			if i < 0 {
				break
			}
			start := from + i
			end := start + len(t.text)
			if termStandsAlone(lower, start, end, t.ascii) {
				out = append(out, start)
				if len(out) >= limit {
					sort.Ints(out)
					return out
				}
			}
			from = start + 1
		}
	}
	sort.Ints(out)
	return out
}

// hasTerm reports whether the window holds one of the terms.
//
// The offset is where the window starts in the whole text, so the boundary
// check looks at the characters around the window too.
func hasTerm(window string, terms []smugglingTerm, offset int) bool {
	for _, t := range terms {
		from := 0
		for from <= len(window) {
			i := strings.Index(window[from:], t.text)
			if i < 0 {
				break
			}
			start := from + i
			end := start + len(t.text)
			if termStandsAlone(window, start, end, t.ascii) {
				return true
			}
			from = start + 1
		}
	}
	return false
}

// termStandsAlone reports whether the match is a whole word.
//
// Only English terms are checked. Korean is written without spaces, so a
// Korean term that sits inside a longer word is still the term.
func termStandsAlone(text string, start, end int, ascii bool) bool {
	if !ascii {
		return true
	}
	if start > 0 && isWordByte(text[start-1]) {
		return false
	}
	if end < len(text) && isWordByte(text[end]) {
		return false
	}
	return true
}

// isWordByte reports whether the byte is part of an English word.
func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// sourceOf names where a text came from.
func sourceOf(ev *event.Event) string {
	switch ev.Kind {
	case event.KindPrompt:
		return "prompt"
	case event.KindMessage:
		return "message"
	case event.KindTool:
		return "tool_output"
	}
	return "other"
}

// smuggling scans the assembled context for a deception instruction.
//
// The context is what the agent actually saw: the user's prompt, the
// assistant's own words, and the output of every tool call, including the
// contents of files it read and the memory files it loaded.
func (e *Engine) smuggling(ev *event.Event, sigs *[]Signal) {
	if ev.Text == "" {
		return
	}
	kind, ok := ScanSmuggling(ev.Text)
	if !ok {
		return
	}
	e.stashSmuggling(SmugglingHit{Seq: ev.Seq, Rule: ruleForKind(kind), Kind: kind, Source: sourceOf(ev)})
	e.add(sigs, Signal{Detector: "S9", Rule: ruleForKind(kind), Confidence: 0.7, Level: L2, Evidence: []int64{ev.Seq},
		Facts: map[string]any{"kind": kind, "source": sourceOf(ev)}})
}

// stashSmuggling keeps a hit, up to the session's limit.
func (e *Engine) stashSmuggling(hit SmugglingHit) {
	if len(e.St.smuggling) >= maxSmugglingHits {
		return
	}
	e.St.smuggling = append(e.St.smuggling, hit)
}

// smugglingPatch looks for a deception instruction in what the agent is
// about to write.
//
// The agent planting the instruction in a file is the one case where the
// monitor can stop it outright: the text is the agent's own new text, seen
// before it lands.
func (e *Engine) smugglingPatch(ev *event.Event) *Signal {
	if ev.Tool != event.ToolWrite && ev.Tool != event.ToolEdit {
		return nil
	}
	for _, pf := range ev.Patch {
		for _, line := range pf.Added {
			kind, ok := ScanSmuggling(line)
			if !ok {
				continue
			}
			e.stashSmuggling(SmugglingHit{Seq: ev.Seq, Rule: ruleForKind(kind), Kind: kind, Source: "written", Written: true})
			return &Signal{Detector: "S9", Rule: ruleForKind(kind), Confidence: 0.8, Level: L2, Evidence: []int64{ev.Seq},
				Facts: map[string]any{"kind": kind, "source": "written", "path": pf.Path, "blocked": true}}
		}
	}
	return nil
}

// SmugglingHits returns the session's deception instructions.
func (e *Engine) SmugglingHits() []SmugglingHit { return e.St.smuggling }
