package fp

import (
	"path"
	"sort"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
)

// ErrorBlock is one extracted error: kind, message template and top frames.
type ErrorBlock struct {
	Kind     string
	Template string
	Frames   []string // "file:func", at most 3
}

// FP returns the error fingerprint.
func (e ErrorBlock) FP() string {
	return Hash("err", e.Kind, e.Template, strings.Join(e.Frames, ";"))
}

var (
	tsRe      = lazyre.New(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?|\b\d{2}:\d{2}:\d{2}(?:\.\d+)?\b`)
	hexRe     = lazyre.New(`\b0x[0-9a-fA-F]+\b|\b[0-9a-fA-F]{8,}\b`)
	pathRe    = lazyre.New(`(?:[A-Za-z]:)?(?:\.{0,2}/|[A-Za-z0-9_.@-]+/)(?:[A-Za-z0-9_.@-]+/)*([A-Za-z0-9_.@-]+)`)
	numRe     = lazyre.New(`\b\d+(?:\.\d+)?\b`)
	quotedRe  = lazyre.New(`'[^']{33,}'|"[^"]{33,}"`)
	ansiRe    = lazyre.New(`\x1b\[[0-9;]*[A-Za-z]`)
	pyFrameRe = lazyre.New(`^\s*File "([^"]+)", line \d+, in (\S+)`)
	pyExcRe   = lazyre.New(`^([A-Za-z_][A-Za-z0-9_.]*(?:Error|Exception|Exit|Interrupt|Warning|Failure))(?::\s*(.*))?$`)
	nodeAtRe  = lazyre.New(`^\s+at (?:(\S+) \()?([^():]+):\d+:\d+\)?`)
	nodeErrRe = lazyre.New(`^(?:Uncaught )?([A-Z][A-Za-z]*Error|Error)(?: \[[A-Z_]+\])?: (.*)$`)
	goPanicRe = lazyre.New(`^panic: (.*)$`)
	goFailRe  = lazyre.New(`^--- FAIL: (\S+)`)
	goFrameRe = lazyre.New(`^([a-zA-Z0-9_./-]+)\.([A-Za-z0-9_]+)\(.*\)$`)
	rustErrRe = lazyre.New(`^error(\[E\d+\])?: (.*)$`)
	tscErrRe  = lazyre.New(`^(.*?)\(\d+,\d+\): error (TS\d+): (.*)$|^(.*?):\d+:\d+ - error (TS\d+): (.*)$`)
	javaExcRe = lazyre.New(`^(?:Exception in thread "[^"]*" )?(?:Caused by: )?([a-z][a-zA-Z0-9_]*(?:\.[a-zA-Z0-9_$]+)+(?:Exception|Error))(?::\s*(.*))?$`)
	javaAtRe  = lazyre.New(`^\s+at ([\w$.]+)\.([\w$<>]+)\(([^:)]+)(?::\d+)?\)`)
	javacRe   = lazyre.New(`^(.*\.(?:java|kt)):\d+: error: (.*)$|^e: (?:file://)?(.*\.kt):\d+:\d+ (.*)$`)
	genericRe = lazyre.New(`^(?:\[?ERROR\]?|FATAL|fatal|Error|error)[:\s]\s*(.*)$`)
)

// Template masks volatile tokens of a message.
func Template(msg string) string {
	s := ansiRe.ReplaceAllString(msg, "")
	s = strings.TrimSpace(s)
	s = tsRe.ReplaceAllString(s, "<t>")
	s = quotedRe.ReplaceAllString(s, "<s>")
	s = pathRe.ReplaceAllStringFunc(s, func(m string) string {
		if !strings.Contains(m, "/") {
			return m
		}
		return "<path>/" + path.Base(m)
	})
	s = hexRe.ReplaceAllString(s, "<hex>")
	s = numRe.ReplaceAllString(s, "<n>")
	s = spaceRe.ReplaceAllString(s, " ")
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// errorHints are substrings at least one of which every recognized error
// header line contains; other lines skip the regular expressions.
var errorHints = []string{"rror", "RROR", "xception", "FAIL", "panic", "FATAL", "atal", "Traceback", "Interrupt", "Exit", "Warning", "Failure", "e: "}

func mightBeError(line string) bool {
	for _, h := range errorHints {
		if strings.Contains(line, h) {
			return true
		}
	}
	return false
}

// ExtractErrors pulls error blocks out of tool output.
func ExtractErrors(out string) []ErrorBlock {
	if out == "" {
		return nil
	}
	out = ansiRe.ReplaceAllString(out, "")
	lines := strings.Split(out, "\n")
	var blocks []ErrorBlock
	var pyFrames []string
	for i := 0; i < len(lines); i++ {
		ln := strings.TrimRight(lines[i], "\r")
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if strings.Contains(ln, `File "`) {
			if m := pyFrameRe.FindStringSubmatch(ln); m != nil {
				pyFrames = append(pyFrames, path.Base(m[1])+":"+m[2])
				continue
			}
		}
		if !mightBeError(t) {
			continue
		}
		if strings.HasPrefix(t, "Traceback (most recent call last)") {
			pyFrames = nil
			continue
		}
		if m := pyExcRe.FindStringSubmatch(t); m != nil && len(pyFrames) > 0 {
			blocks = append(blocks, ErrorBlock{Kind: m[1], Template: Template(m[2]), Frames: lastN(pyFrames, 3)})
			pyFrames = nil
			continue
		}
		if m := javaExcRe.FindStringSubmatch(t); m != nil {
			var fr []string
			for j := i + 1; j < len(lines) && len(fr) < 3; j++ {
				if fm := javaAtRe.FindStringSubmatch(lines[j]); fm != nil {
					fr = append(fr, fm[3]+":"+fm[2])
				} else if strings.TrimSpace(lines[j]) != "" && !strings.HasPrefix(strings.TrimSpace(lines[j]), "...") {
					break
				}
			}
			blocks = append(blocks, ErrorBlock{Kind: m[1], Template: Template(m[2]), Frames: fr})
			continue
		}
		if m := nodeErrRe.FindStringSubmatch(t); m != nil {
			var fr []string
			for j := i + 1; j < len(lines) && len(fr) < 3; j++ {
				if fm := nodeAtRe.FindStringSubmatch(lines[j]); fm != nil {
					fr = append(fr, path.Base(fm[2])+":"+fm[1])
				} else {
					break
				}
			}
			blocks = append(blocks, ErrorBlock{Kind: m[1], Template: Template(m[2]), Frames: fr})
			continue
		}
		if m := goPanicRe.FindStringSubmatch(t); m != nil {
			var fr []string
			for j := i + 1; j < len(lines) && len(fr) < 3; j++ {
				if fm := goFrameRe.FindStringSubmatch(strings.TrimSpace(lines[j])); fm != nil {
					fr = append(fr, path.Base(fm[1])+":"+fm[2])
				}
			}
			blocks = append(blocks, ErrorBlock{Kind: "panic", Template: Template(m[1]), Frames: fr})
			continue
		}
		if m := goFailRe.FindStringSubmatch(t); m != nil {
			// go test failure: the first indented message line is the template
			msg := ""
			for j := i + 1; j < len(lines); j++ {
				x := strings.TrimSpace(lines[j])
				if x == "" || strings.HasPrefix(x, "---") || strings.HasPrefix(x, "===") || x == "FAIL" {
					break
				}
				msg = x
				break
			}
			blocks = append(blocks, ErrorBlock{Kind: "go_test_fail", Template: Template(m[1] + " " + msg)})
			continue
		}
		if m := rustErrRe.FindStringSubmatch(t); m != nil && !strings.HasPrefix(t, "error: could not compile") && !strings.HasPrefix(t, "error: aborting") {
			blocks = append(blocks, ErrorBlock{Kind: "rustc" + m[1], Template: Template(m[2])})
			continue
		}
		if m := tscErrRe.FindStringSubmatch(t); m != nil {
			code, msg, file := m[2], m[3], m[1]
			if code == "" {
				code, msg, file = m[5], m[6], m[4]
			}
			blocks = append(blocks, ErrorBlock{Kind: code, Template: Template(msg), Frames: []string{path.Base(file)}})
			continue
		}
		if m := javacRe.FindStringSubmatch(t); m != nil {
			file, msg := m[1], m[2]
			if file == "" {
				file, msg = m[3], m[4]
			}
			blocks = append(blocks, ErrorBlock{Kind: "compile", Template: Template(msg), Frames: []string{path.Base(file)}})
			continue
		}
		// pytest assertion lines ("E   AssertionError: ...") and bare exceptions
		if m := pyExcRe.FindStringSubmatch(strings.TrimSpace(strings.TrimPrefix(t, "E "))); m != nil && m[2] != "" {
			blocks = append(blocks, ErrorBlock{Kind: m[1], Template: Template(m[2])})
			continue
		}
		if m := genericRe.FindStringSubmatch(t); m != nil && len(blocks) < 20 {
			msg := m[1]
			if len(strings.TrimSpace(msg)) < 3 {
				continue
			}
			blocks = append(blocks, ErrorBlock{Kind: "error", Template: Template(msg)})
		}
	}
	return dedupBlocks(blocks)
}

// ErrorFPs returns the sorted unique error fingerprints of an output.
func ErrorFPs(out string) []string {
	bs := ExtractErrors(out)
	set := map[string]bool{}
	for _, b := range bs {
		set[b.FP()] = true
	}
	res := make([]string, 0, len(set))
	for k := range set {
		res = append(res, k)
	}
	sort.Strings(res)
	return res
}

func lastN(s []string, n int) []string {
	if len(s) <= n {
		return append([]string(nil), s...)
	}
	return append([]string(nil), s[len(s)-n:]...)
}

func dedupBlocks(bs []ErrorBlock) []ErrorBlock {
	seen := map[string]bool{}
	out := bs[:0]
	for _, b := range bs {
		k := b.FP()
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, b)
	}
	return out
}
