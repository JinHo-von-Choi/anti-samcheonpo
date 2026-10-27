package detect

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// RuleFixation is work concentrated on one error, file or command without
// progress, while targets the user named are still untouched.
//
// Concentration alone is normal: fixing one bug means many steps on one
// file. The rule needs all three facts together: the concentration, no
// progress inside it, and a named target of the request left untouched.
// It advises and never blocks; the judgment of what matters is the user's.
const RuleFixation = "s3.fixation"

const (
	fixationWindow = 12
	fixationShare  = 9
	// recentMessages bounds the agent messages the dominant-term check reads.
	recentMessages = 6
)

type fixAction struct {
	seq      int64
	focus    string
	progress bool
}

// focusOf names what one tool call works on.
func focusOf(ev *event.Event) string {
	failed := ev.ExitCode != nil && *ev.ExitCode != 0 || ev.IsError
	switch {
	case failed && errKey(ev) != "":
		return "error:" + errKey(ev)
	case len(ev.Paths) > 0:
		return "path:" + ev.Paths[0]
	case ev.CmdFP != "":
		return "cmd:" + ev.CmdFP
	}
	return ""
}

// errKey identifies the failure of one run: its error fingerprint, or the
// result fingerprint, or the command when the output gave neither.
func errKey(ev *event.Event) string {
	switch {
	case len(ev.ErrFPs) > 0:
		return ev.ErrFPs[0]
	case ev.ResultFP != "":
		return ev.ResultFP
	}
	return ev.CmdFP
}

// observeFixation records one tool call and judges the window.
func (e *Engine) observeFixation(ev *event.Event, sigs *[]Signal) {
	st := e.St
	switch ev.Kind {
	case event.KindPrompt:
		if !ev.Forced {
			st.fixActions, st.fixFired = nil, nil
		}
		return
	case event.KindMessage:
		if ev.Text != "" {
			st.recentMsgs = append(st.recentMsgs, ev.Text)
			if len(st.recentMsgs) > recentMessages {
				st.recentMsgs = st.recentMsgs[1:]
			}
		}
		return
	case event.KindTool:
	default:
		return
	}
	if ev.Background {
		return
	}
	focus := focusOf(ev)
	if focus == "" {
		return
	}
	writing := (ev.Tool == event.ToolWrite || ev.Tool == event.ToolEdit) && !ev.IsError
	if writing && st.fixOpenErr != "" {
		// an edit while an error is open is part of fixing that error
		focus = "error:" + st.fixOpenErr
	}
	progress := false
	if ev.Category == event.CatVerify && ev.ExitCode != nil && *ev.ExitCode == 0 && st.fixLastFailing {
		progress = true // a failing check now passes
	}
	if ev.Category == event.CatVerify && ev.ExitCode != nil {
		st.fixLastFailing = *ev.ExitCode != 0
	}
	if ev.Tool == event.ToolShell && ev.ExitCode != nil {
		// any run can open an error (a linter is not always a verification);
		// a successful run of the same command or a passing check closes it
		switch {
		case *ev.ExitCode != 0 && errKey(ev) != "":
			st.fixOpenErr, st.fixOpenCmd = errKey(ev), ev.CmdFP
		case *ev.ExitCode == 0 && (ev.CmdFP == st.fixOpenCmd || ev.Category == event.CatVerify):
			st.fixOpenErr, st.fixOpenCmd = "", ""
		}
	}
	if writing {
		if st.fixWritten == nil {
			st.fixWritten = map[string]bool{}
		}
		for _, p := range ev.Paths {
			if !st.fixWritten[p] && !IsDocPath(p) {
				progress = true // a new artifact
			}
			st.fixWritten[p] = true
		}
	}
	st.fixActions = append(st.fixActions, fixAction{seq: ev.Seq, focus: focus, progress: progress})
	if len(st.fixActions) > fixationWindow {
		st.fixActions = st.fixActions[len(st.fixActions)-fixationWindow:]
	}
	if len(st.fixActions) < fixationWindow {
		return
	}
	counts := map[string]int{}
	for _, a := range st.fixActions {
		if a.progress {
			return
		}
		counts[a.focus]++
	}
	top, n := "", 0
	for f, c := range counts {
		if c > n || c == n && f < top {
			top, n = f, c
		}
	}
	if n < fixationShare || st.fixFired[top] {
		return
	}
	requests := e.RequestText()
	var missing []string
	for _, id := range requestIdents(requests) {
		if !st.changed(id) && !strings.Contains(strings.ToLower(top), id) {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return
	}
	if st.fixFired == nil {
		st.fixFired = map[string]bool{}
	}
	st.fixFired[top] = true
	evidence := make([]int64, 0, len(st.fixActions))
	for _, a := range st.fixActions {
		if a.focus == top {
			evidence = append(evidence, a.seq)
		}
	}
	kind, target, _ := strings.Cut(top, ":")
	facts := map[string]any{"kind": kind, "count": n, "window": fixationWindow, "missing": strings.Join(missing, ", ")}
	if kind == "path" {
		facts["path"] = target
	}
	if term := dominantTerm(st.recentMsgs, requests); term != "" {
		facts["dominant_term"] = term
	}
	st.fixActions = nil
	e.add(sigs, Signal{Detector: "S3", Rule: RuleFixation, Confidence: 0.6, Level: L1, Evidence: evidence, Facts: facts})
}

// dominantTerm returns the word the agent keeps repeating in its recent
// messages when the user's requests never use it, or "".
//
// It is supporting evidence for a fixation, never a verdict on its own:
// a repeated word is often just the name of the thing being fixed.
func dominantTerm(messages []string, requests string) string {
	counts := map[string]int{}
	for _, m := range messages {
		seen := map[string]bool{}
		for _, w := range strings.FieldsFunc(strings.ToLower(m), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }) {
			if seen[w] || !termWorthy(w) {
				continue
			}
			seen[w] = true
			counts[w]++
		}
	}
	lower := strings.ToLower(requests)
	var words []string
	for w, c := range counts {
		if c >= recentMessages-1 && !strings.Contains(lower, w) {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return ""
	}
	sort.Slice(words, func(i, j int) bool {
		if counts[words[i]] != counts[words[j]] {
			return counts[words[i]] > counts[words[j]]
		}
		return words[i] < words[j]
	})
	return words[0]
}

// termWorthy keeps words long enough to name a subject.
func termWorthy(w string) bool {
	n := utf8.RuneCountInString(w)
	r, _ := utf8.DecodeRuneInString(w)
	if r < utf8.RuneSelf {
		return n >= 4
	}
	return n >= 2
}
