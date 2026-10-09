package classify

import (
	"regexp"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
)

// heredoc is one here-document: the program that reads it, the file a
// redirect writes it to (cat > f <<EOF), and its body.
type heredoc struct {
	reader string
	target string
	body   string
}

var (
	heredocMarkRe = regexp.MustCompile(`<<(-?)\s*(?:'([A-Za-z_][\w-]*)'|"([A-Za-z_][\w-]*)"|([A-Za-z_][\w-]*))`)
	heredocDestRe = regexp.MustCompile(`>{1,2}\s*([^\s<>;&|]+)`)
)

// splitHeredocs separates a shell command's here-documents from the command
// text around them. Each body is returned with the program of the stage that
// reads it; the outer text keeps the marker line and drops the body, so body
// lines are never classified as commands of their own.
func splitHeredocs(cmd string) (outer string, docs []heredoc) {
	if !strings.Contains(cmd, "<<") {
		return cmd, nil
	}
	lines := strings.Split(cmd, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		out = append(out, line)
		marks := heredocMarkRe.FindAllStringSubmatchIndex(line, -1)
		for _, m := range marks {
			if m[0] > 0 && line[m[0]-1] == '<' {
				continue // <<< here-string
			}
			tag := ""
			for _, g := range []int{4, 6, 8} {
				if m[g] >= 0 {
					tag = line[m[g]:m[g+1]]
				}
			}
			dash := m[3] > m[2]
			stage := lastStage(line[:m[0]])
			d := heredoc{reader: stageProgram(stage)}
			if t := heredocDestRe.FindStringSubmatch(stage + " " + line[m[1]:]); t != nil {
				d.target = strings.Trim(t[1], `'"`)
			}
			var body []string
			for i+1 < len(lines) {
				i++
				l := lines[i]
				if l == tag || (dash && strings.TrimLeft(l, "\t") == tag) {
					break
				}
				body = append(body, l)
			}
			d.body = strings.Join(body, "\n")
			docs = append(docs, d)
		}
	}
	return strings.Join(out, "\n"), docs
}

// lastStage returns the shell stage that ends a line fragment.
func lastStage(s string) string {
	st := fp.SplitStages(s)
	if len(st) == 0 {
		return ""
	}
	return st[len(st)-1]
}

// stageProgram is the bare program name of a shell stage, past environment
// assignments and wrappers.
func stageProgram(stage string) string {
	n, _ := fp.NormalizeCmd(stage)
	w := strings.Fields(bareProgram(n))
	for len(w) > 0 && (w[0] == "sudo" || w[0] == "env" || w[0] == "exec" || w[0] == "nohup" || w[0] == "time" || w[0] == "setsid") {
		w = w[1:]
		if len(w) > 0 {
			w[0] = pathnorm.CommandBase(w[0])
		}
	}
	if len(w) == 0 {
		return ""
	}
	return bareProgram(w[0])
}

// interpreter kinds that run a here-document as a program.
func interpreterKind(prog string) string {
	switch {
	case prog == "python" || prog == "python3" || prog == "py" || prog == "pypy3" || prog == "uv" || prog == "poetry":
		return "python"
	case prog == "node" || prog == "deno" || prog == "bun":
		return "node"
	case prog == "bash" || prog == "sh" || prog == "zsh" || prog == "dash" || prog == "ksh":
		return "shell"
	}
	return ""
}

var (
	pyArgvCallRe   = regexp.MustCompile(`subprocess\.(?:run|call|check_call|check_output|Popen)\(\s*\[([^\]]*)\]`)
	pyShellCallRe  = regexp.MustCompile(`(?:subprocess\.(?:run|call|check_call|check_output|Popen)|os\.system|os\.popen)\(\s*(?:f?'([^'\n]*)'|f?"([^"\n]*)")`)
	nodeExecRe     = regexp.MustCompile("(?:execSync|exec|spawnSync|execFileSync)\\(\\s*(?:'([^'\\n]*)'|\"([^\"\\n]*)\"|`([^`\\n]*)`)\\s*(?:,\\s*\\[([^\\]]*)\\])?")
	argvItemRe     = regexp.MustCompile(`f?'([^']*)'|f?"([^"]*)"|([A-Za-z_][\w.]*(?:\[[^\]]*\])?)`)
	pyWriteRe      = regexp.MustCompile(`([A-Za-z_]\w*)\s*=\s*Path\(\s*['"]([^'"]+)['"]\s*\)|Path\(\s*['"]([^'"]+)['"]\s*\)\s*\.\s*write_(?:text|bytes)\(|\b([A-Za-z_]\w*)\s*\.\s*write_(?:text|bytes)\(|open\(\s*['"]([^'"]+)['"]\s*,\s*['"][wax]`)
	pyWriteCallRe  = regexp.MustCompile(`\.write_(?:text|bytes)\(|open\([^)]*['"][wax]b?\+?['"]|shutil\.(?:copy|move)`)
	pythonIdentsRe = regexp.MustCompile(`(?i)python|sys\.executable`)
)

// argvCommand joins a Python or JavaScript argument list into a command line.
// A variable naming the interpreter becomes "python"; other variables stay as
// a placeholder.
func argvCommand(list string) string {
	var parts []string
	for _, m := range argvItemRe.FindAllStringSubmatch(list, -1) {
		switch {
		case m[1] != "" || strings.HasPrefix(m[0], "'") || strings.HasPrefix(m[0], "f'"):
			parts = append(parts, m[1])
		case m[2] != "" || strings.HasPrefix(m[0], `"`) || strings.HasPrefix(m[0], `f"`):
			parts = append(parts, m[2])
		case pythonIdentsRe.MatchString(m[3]):
			parts = append(parts, "python")
		default:
			parts = append(parts, "<"+m[3]+">")
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// embeddedCommands lists the commands a here-document program runs: the
// argument lists and command strings of subprocess and os.system calls in
// Python, exec calls in JavaScript, and the lines of a shell script.
func embeddedCommands(d heredoc) []string {
	var out []string
	switch interpreterKind(d.reader) {
	case "python":
		for _, m := range pyArgvCallRe.FindAllStringSubmatch(d.body, -1) {
			if c := argvCommand(m[1]); c != "" {
				out = append(out, c)
			}
		}
		for _, m := range pyShellCallRe.FindAllStringSubmatch(d.body, -1) {
			if c := strings.TrimSpace(m[1] + m[2]); c != "" {
				out = append(out, c)
			}
		}
	case "node":
		for _, m := range nodeExecRe.FindAllStringSubmatch(d.body, -1) {
			c := strings.TrimSpace(m[1] + m[2] + m[3])
			if m[4] != "" {
				c = strings.TrimSpace(c + " " + argvCommand(m[4]))
			}
			if c != "" {
				out = append(out, c)
			}
		}
	case "shell":
		if strings.TrimSpace(d.body) != "" {
			out = append(out, d.body)
		}
	}
	return out
}

// pythonWrites lists the files a Python here-document writes with
// Path(...).write_text/write_bytes or open(..., "w"), in source order, so a
// variable reassigned to another path is followed.
func pythonWrites(body string) []string {
	if !pyWriteCallRe.MatchString(body) {
		return nil
	}
	vars := map[string]string{}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, m := range pyWriteRe.FindAllStringSubmatch(body, -1) {
		switch {
		case m[1] != "":
			vars[m[1]] = m[2]
		case m[3] != "":
			add(m[3])
		case m[4] != "":
			add(vars[m[4]])
		case m[5] != "":
			add(m[5])
		}
	}
	return out
}

// HeredocWrites lists the project files a shell command writes from inside
// here-documents: Python scripts that write files, and cat/tee redirects of a
// body into a file. Absolute and temporary paths are left out: they are not
// the project's source as the command spells it.
func HeredocWrites(cmd string) []string {
	_, docs := splitHeredocs(cmd)
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimPrefix(p, "./")
		if p == "" || seen[p] || pathnorm.IsAbs(p) || pathnorm.IsTempPath(p) || strings.HasPrefix(p, "~") || strings.Contains(p, "$") {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, d := range docs {
		if d.target != "" && (d.reader == "cat" || d.reader == "tee") {
			add(d.target)
		}
		if interpreterKind(d.reader) == "python" {
			for _, p := range pythonWrites(d.body) {
				add(p)
			}
		}
	}
	return out
}

// HeredocFiles maps the files a command writes from a here-document through
// cat or tee to the written content, so a script written now can be judged by
// its content when it runs later.
func HeredocFiles(cmd string) map[string]string {
	_, docs := splitHeredocs(cmd)
	var out map[string]string
	for _, d := range docs {
		if d.target == "" || (d.reader != "cat" && d.reader != "tee") {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[d.target] = d.body
	}
	return out
}

// EmbeddedChecks lists the verification commands a here-document program or
// a script body runs (subprocess calls of pytest or a gate script).
func EmbeddedChecks(cmd string) []string {
	_, docs := splitHeredocs(cmd)
	var out []string
	for _, d := range docs {
		for _, c := range embeddedCommands(d) {
			if cls, _ := Shell(c, Options{}); cls == ShellVerify {
				out = append(out, c)
			}
		}
	}
	return out
}

// ScriptChecks lists the verification commands a script file's content runs,
// judged as a here-document read by the script's interpreter.
func ScriptChecks(path, body string) []string {
	reader := "python"
	switch {
	case strings.HasSuffix(path, ".sh") || strings.HasSuffix(path, ".bash") || strings.HasPrefix(body, "#!/bin/bash") || strings.HasPrefix(body, "#!/bin/sh"):
		reader = "bash"
	case strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".mjs") || strings.HasSuffix(path, ".ts"):
		reader = "node"
	case !strings.HasSuffix(path, ".py") && !strings.HasPrefix(body, "#!/usr/bin/env python") && !strings.HasPrefix(body, "#!/usr/bin/python"):
		return nil
	}
	var out []string
	for _, c := range embeddedCommands(heredoc{reader: reader, body: body}) {
		if cls, _ := Shell(c, Options{}); cls == ShellVerify {
			out = append(out, c)
		}
	}
	return out
}

// TestCommands lists the test runs a shell command makes: test runner and
// gate-script stages of the command, and the ones its here-documents run.
// Program paths are reduced to the program name.
func TestCommands(raw string) []string {
	outer, docs := splitHeredocs(raw)
	var out []string
	for _, st := range fp.SplitStages(outer) {
		n, _ := fp.NormalizeCmd(st)
		if c, _ := stage(st, Options{}); c == ShellVerify && isTestRun(st) {
			out = append(out, bareProgram(n))
		}
	}
	for _, d := range docs {
		for _, c := range embeddedCommands(d) {
			for _, st := range fp.SplitStages(c) {
				n, _ := fp.NormalizeCmd(st)
				if cls, _ := stage(st, Options{}); cls == ShellVerify && isTestRun(st) {
					out = append(out, bareProgram(n))
				}
			}
		}
	}
	return out
}

// scriptSelectFlags select part of what a gate or test script runs.
var scriptSelectFlags = map[string]bool{"--gate": true, "--only": true, "--select": true, "-k": true, "-t": true, "--test": true, "--tests": true,
	"--filter": true, "--suite": true, "--case": true, "--step": true}

// suiteScriptRe names scripts that run a whole gate or suite.
var suiteScriptRe = regexp.MustCompile(`(?i)(?:^|[/_.-])(?:gates?|ci|all|full|suite|regression)(?:[/_.-]|$)`)

// CommandScope is VerifyScope for one command, extended to project gate and
// test scripts: such a script with no selecting argument runs all of it.
// A plain number argument (a timeout, a version) selects nothing.
func CommandScope(c string) string {
	n, _ := fp.NormalizeCmd(c)
	if s := VerifyScope(n); s != ScopeUnknown {
		return s
	}
	b := bareProgram(n)
	m := verifyScriptRe.FindString(b)
	if m == "" {
		return ScopeUnknown
	}
	if !suiteScriptRe.MatchString(m) {
		// a check script of another name may check one thing; how long it
		// runs decides instead (expensive_check_sec)
		return ScopeUnknown
	}
	for _, a := range strings.Fields(strings.TrimSpace(b[len(m):])) {
		name := a
		if i := strings.IndexByte(a, '='); i > 0 {
			name = a[:i]
		}
		if scriptSelectFlags[name] {
			return ScopeTargeted
		}
		if !strings.HasPrefix(a, "-") && strings.Trim(a, "0123456789.") != "" && !strings.HasPrefix(a, "<") {
			return ScopeTargeted
		}
	}
	return ScopeFull
}

// ExecutedScripts lists the script files a command runs: the first argument
// of an interpreter (python /tmp/x.py, bash run.sh), or the command word
// itself (./run.sh). A script only read (cat x.py) is not run.
func ExecutedScripts(norm string) []string {
	var out []string
	for _, st := range fp.SplitStages(norm) {
		n, _ := fp.NormalizeCmd(st)
		w := strings.Fields(n)
		for len(w) > 0 && (w[0] == "sudo" || w[0] == "env" || w[0] == "exec" || w[0] == "nohup" || w[0] == "time" || w[0] == "setsid" || strings.Contains(w[0], "=")) {
			w = w[1:]
		}
		if len(w) == 0 {
			continue
		}
		if interpreterKind(bareProgram(w[0])) == "" {
			if strings.Contains(w[0], "/") || strings.Contains(w[0], ".") {
				out = append(out, strings.Trim(w[0], `'"`))
			}
			continue
		}
		for _, a := range w[1:] {
			if a == "-m" || a == "-c" || a == "-" {
				break // a module, inline code or stdin, not a file
			}
			if strings.HasPrefix(a, "-") {
				continue
			}
			out = append(out, strings.Trim(a, `'"`))
			break
		}
	}
	return out
}

// RunnableText is a command's text with each here-document replaced by what
// it runs: a body read by a shell stays as script lines, a body read by
// Python or JavaScript is reduced to the commands it runs, and a body that is
// content (cat > file <<EOF) is dropped. Checks that look for commands in
// command position read this, so text inside a written file is not a command.
func RunnableText(cmd string) string {
	outer, docs := splitHeredocs(cmd)
	if len(docs) == 0 {
		return cmd
	}
	parts := []string{outer}
	for _, d := range docs {
		switch interpreterKind(d.reader) {
		case "shell":
			parts = append(parts, RunnableText(d.body))
		case "python", "node":
			parts = append(parts, embeddedCommands(d)...)
		}
	}
	return strings.Join(parts, "\n")
}
