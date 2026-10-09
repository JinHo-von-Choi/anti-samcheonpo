package classify

import (
	"regexp"
	"strings"
)

// ScriptWrite is one file a Python here-document writes with write_text:
// either literal content, or the content of a file it read (From, possibly
// the same file), with the literal .replace(old, new) edits applied to it.
type ScriptWrite struct {
	Path     string
	Content  string
	Literal  bool
	From     string
	Replaces [][2]string
}

var (
	pyAssignPathRe = regexp.MustCompile(`([A-Za-z_]\w*)\s*=\s*Path\(\s*(?:'([^'\n]+)'|"([^"\n]+)")\s*\)`)
	pyAssignReadRe = regexp.MustCompile(`([A-Za-z_]\w*)\s*=\s*(?:Path\(\s*(?:'([^'\n]+)'|"([^"\n]+)")\s*\)|([A-Za-z_]\w*))\.read_text\(\s*\)`)
	pyWriteTextRe  = regexp.MustCompile(`(?:Path\(\s*(?:'([^'\n]+)'|"([^"\n]+)")\s*\)|([A-Za-z_]\w*))\.write_text\(\s*`)
	pyReadExprRe   = regexp.MustCompile(`^(?:Path\(\s*(?:'([^'\n]+)'|"([^"\n]+)")\s*\)|([A-Za-z_]\w*))\.read_text\(\s*\)`)
	pyReplaceRe    = regexp.MustCompile(`^\s*\.replace\(\s*`)
	pyReassignRe   = regexp.MustCompile(`([A-Za-z_]\w*)\s*=\s*([A-Za-z_]\w*)\s*\.replace\(`)
)

// PythonScriptWrites reads the write_text calls of a Python program in source
// order. Variables holding a Path or the text read from one are followed, so
// `s = Path(a).read_text().replace(x, y); Path(b).write_text(s)` is a copy of
// a into b with one edit. Anything it cannot read exactly is left out.
func PythonScriptWrites(body string) []ScriptWrite {
	type textVar struct {
		from     string
		replaces [][2]string
		content  string
		literal  bool
	}
	paths := map[string]string{}
	texts := map[string]textVar{}
	var out []ScriptWrite
	type tok struct {
		at   int
		kind int
		m    []int
	}
	var toks []tok
	for _, m := range pyAssignPathRe.FindAllStringSubmatchIndex(body, -1) {
		toks = append(toks, tok{m[0], 0, m})
	}
	for _, m := range pyAssignReadRe.FindAllStringSubmatchIndex(body, -1) {
		toks = append(toks, tok{m[0], 1, m})
	}
	for _, m := range pyWriteTextRe.FindAllStringSubmatchIndex(body, -1) {
		toks = append(toks, tok{m[0], 2, m})
	}
	for _, m := range pyReassignRe.FindAllStringSubmatchIndex(body, -1) {
		toks = append(toks, tok{m[0], 3, m})
	}
	sortToks := func() {
		for i := 1; i < len(toks); i++ {
			for j := i; j > 0 && toks[j].at < toks[j-1].at; j-- {
				toks[j], toks[j-1] = toks[j-1], toks[j]
			}
		}
	}
	sortToks()
	group := func(m []int, g int) string {
		if m[2*g] < 0 {
			return ""
		}
		return body[m[2*g]:m[2*g+1]]
	}
	pathOf := func(lit1, lit2, v string) string {
		if lit1 != "" {
			return lit1
		}
		if lit2 != "" {
			return lit2
		}
		return paths[v]
	}
	for _, t := range toks {
		m := t.m
		switch t.kind {
		case 0:
			paths[group(m, 1)] = group(m, 2) + group(m, 3)
		case 1:
			from := pathOf(group(m, 2), group(m, 3), group(m, 4))
			reps, _ := readReplaces(body, m[1])
			if from != "" {
				texts[group(m, 1)] = textVar{from: from, replaces: reps}
			}
		case 3:
			// s = t.replace(...): a further edit of text read earlier
			tv, ok := texts[group(m, 2)]
			if !ok {
				continue
			}
			reps, _ := readReplaces(body, m[1]-len(".replace("))
			tv.replaces = append(append([][2]string(nil), tv.replaces...), reps...)
			texts[group(m, 1)] = tv
		case 2:
			dst := pathOf(group(m, 1), group(m, 2), group(m, 3))
			if dst == "" {
				continue
			}
			arg := body[m[1]:]
			if lit, _, ok := pyString(arg, 0); ok {
				out = append(out, ScriptWrite{Path: dst, Content: lit, Literal: true})
				continue
			}
			if rm := pyReadExprRe.FindStringSubmatchIndex(arg); rm != nil {
				from := pathOf(sub(arg, rm, 1), sub(arg, rm, 2), sub(arg, rm, 3))
				reps, _ := readReplaces(arg, rm[1])
				if from != "" {
					out = append(out, ScriptWrite{Path: dst, From: from, Replaces: reps})
				}
				continue
			}
			if id := leadingIdent(arg); id != "" {
				if tv, ok := texts[id]; ok {
					reps, _ := readReplaces(arg, len(id))
					out = append(out, ScriptWrite{Path: dst, From: tv.from, Content: tv.content, Literal: tv.literal, Replaces: append(append([][2]string(nil), tv.replaces...), reps...)})
				}
			}
		}
	}
	return out
}

func sub(s string, m []int, g int) string {
	if m[2*g] < 0 {
		return ""
	}
	return s[m[2*g]:m[2*g+1]]
}

var identRe = regexp.MustCompile(`^[A-Za-z_]\w*`)

func leadingIdent(s string) string {
	id := identRe.FindString(s)
	switch id {
	case "", "Path", "str", "f", "r":
		return ""
	}
	return id
}

// readReplaces reads a chain of .replace(old, new[, count]) calls with
// literal string arguments starting at i.
func readReplaces(s string, i int) ([][2]string, int) {
	var out [][2]string
	for {
		m := pyReplaceRe.FindStringIndex(s[i:])
		if m == nil {
			return out, i
		}
		j := i + m[1]
		a, j2, ok := pyString(s, j)
		if !ok {
			return out, i
		}
		j = skipSpace(s, j2)
		if j >= len(s) || s[j] != ',' {
			return out, i
		}
		b, j3, ok := pyString(s, skipSpace(s, j+1))
		if !ok {
			return out, i
		}
		j = skipSpace(s, j3)
		if j < len(s) && s[j] == ',' {
			// count argument
			for j < len(s) && s[j] != ')' {
				j++
			}
		}
		if j >= len(s) || s[j] != ')' {
			return out, i
		}
		out = append(out, [2]string{a, b})
		i = j + 1
	}
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
		i++
	}
	return i
}

// pyString reads a Python string literal at s[i:] (optional r/b prefix,
// single, double or triple quotes) and returns its value. f-strings are not
// read: their value is not known from the source.
func pyString(s string, i int) (string, int, bool) {
	raw := false
	for i < len(s) && (s[i] == 'r' || s[i] == 'R' || s[i] == 'b' || s[i] == 'B') {
		raw = raw || s[i] == 'r' || s[i] == 'R'
		i++
	}
	if i >= len(s) || (s[i] != '\'' && s[i] != '"') {
		return "", i, false
	}
	q := s[i : i+1]
	if strings.HasPrefix(s[i:], q+q+q) {
		q = q + q + q
	}
	i += len(q)
	var b strings.Builder
	for i < len(s) {
		if strings.HasPrefix(s[i:], q) {
			return b.String(), i + len(q), true
		}
		c := s[i]
		if c == '\n' && len(q) == 1 {
			return "", i, false
		}
		if c == '\\' && i+1 < len(s) {
			if raw {
				b.WriteByte(c)
				b.WriteByte(s[i+1])
				i += 2
				continue
			}
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case '\\', '\'', '"':
				b.WriteByte(s[i+1])
			case '\n':
			default:
				b.WriteByte('\\')
				b.WriteByte(s[i+1])
			}
			i += 2
			continue
		}
		b.WriteByte(c)
		i++
	}
	return "", i, false
}

// PythonHeredocs returns the bodies of Python here-documents in a command.
func PythonHeredocs(cmd string) []string {
	_, docs := splitHeredocs(cmd)
	var out []string
	for _, d := range docs {
		if interpreterKind(d.reader) == "python" {
			out = append(out, d.body)
		}
	}
	return out
}
