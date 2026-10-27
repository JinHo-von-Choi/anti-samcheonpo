package intervene

import (
	"io/fs"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
)

// minOwnSentence is the shortest fixed text registered as an own sentence.
// Shorter pieces such as "형태이며" are ordinary words, and removing them
// from scanned text would remove parts of other people's sentences.
const minOwnSentence = 12

var templateAction = regexp.MustCompile(`\{\{.*?\}\}`)

var recoveryCauses = []recovery.Cause{recovery.Verification, recovery.Code, recovery.Environment, recovery.Scope,
	recovery.Capability, recovery.Memory, recovery.Budget, recovery.Unknown}

func init() {
	detect.SetOwnText(OwnSentences())
}

// OwnSentences returns the fixed lines of every message samcheonpo writes:
// the template text between actions, the rendered smuggling messages and
// the recovery prescriptions.
func OwnSentences() []string {
	seen := map[string]bool{}
	var out []string
	add := func(text string) {
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if utf8.RuneCountInString(line) < minOwnSentence || seen[line] {
				continue
			}
			seen[line] = true
			out = append(out, line)
		}
	}
	files, _ := fs.Glob(tmplFS, "templates/ko/*.tmpl")
	for _, name := range files {
		b, err := tmplFS.ReadFile(name)
		if err != nil {
			continue
		}
		for _, piece := range templateAction.Split(string(b), -1) {
			add(piece)
		}
	}
	for _, c := range recoveryCauses {
		p := recovery.For(c)
		add(p.Action)
		add(p.StopCondition)
	}
	// The smuggling messages name the kind and source they report, and
	// those names read as an instruction themselves. Both sets are finite,
	// so every rendered line is registered.
	for _, rule := range []string{detect.RuleSmuggling, detect.RuleFakeResult} {
		for _, kind := range []string{detect.KindBypassMonitor, detect.KindHideReport, detect.KindFakeResult} {
			for _, source := range []string{"prompt", "message", "tool_output", "written", "other"} {
				v := detect.Signal{Detector: "S9", Rule: rule, Level: detect.L2, Facts: map[string]any{"kind": kind, "source": source}}
				add(Agent(v, Context{}, ""))
				add(User(v, Context{}))
				add(Say(v, Context{}))
			}
		}
	}
	add(recovery.HandoffAction)
	add(recovery.HandoffStop)
	add(Choices)
	return out
}
