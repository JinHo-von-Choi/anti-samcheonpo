package classify

import (
	"path"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
)

// ShellWrites is what a shell command writes, read from its text alone.
// Paths are as the command names them: relative to the command's directory
// or absolute, and possibly glob patterns. PatchFiles are patch files the
// command applies; the files a patch changes are only known from its body.
type ShellWrites struct {
	Paths      []string
	PatchFiles []string
}

// Empty reports whether the command names nothing it writes.
func (w ShellWrites) Empty() bool { return len(w.Paths) == 0 && len(w.PatchFiles) == 0 }

// ShellWriteTargets lists the files a shell command writes or removes:
// output redirections and tee, in-place edits (sed -i, perl -i, ruby -i),
// copy and move destinations (and move sources), rm, truncate, dd of=, git
// checkout/restore/rm/mv paths, patch and git apply, short python -c and
// node -e scripts that write a literal path, and here-documents. A word the
// shell would expand from a variable is left out: the file is not known.
// Anything the text does not state plainly is not guessed.
func ShellWriteTargets(cmd string) ShellWrites {
	var w ShellWrites
	seenP, seenF := map[string]bool{}, map[string]bool{}
	add := func(p string) {
		p = strings.Trim(p, `'"`)
		if p == "" || seenP[p] || strings.ContainsAny(p, "$`") || strings.HasPrefix(p, "/dev/") || strings.EqualFold(p, "nul") || strings.HasPrefix(p, "~") {
			return
		}
		seenP[p] = true
		w.Paths = append(w.Paths, p)
	}
	addPatch := func(p string) {
		p = strings.Trim(p, `'"`)
		if p == "" || p == "-" || seenF[p] || strings.ContainsAny(p, "$`") {
			return
		}
		seenF[p] = true
		w.PatchFiles = append(w.PatchFiles, p)
	}
	outer, docs := splitHeredocs(cmd)
	for _, d := range docs {
		if d.target != "" && (d.reader == "cat" || d.reader == "tee") {
			add(d.target)
		}
		switch {
		case interpreterKind(d.reader) == "python":
			for _, p := range pythonWrites(d.body) {
				add(p)
			}
		case d.reader == "patch" || d.reader == "git":
			for _, p := range PatchTargets(d.body) {
				add(p)
			}
		}
	}
	for _, st := range fp.SplitStages(outer) {
		words := shellWords(st)
		words = dropPrefixes(words)
		var rest []string
		for i := 0; i < len(words); i++ {
			a := strings.TrimLeft(words[i], "0123456789&")
			if strings.HasPrefix(a, ">") {
				t := strings.TrimLeft(a, ">|")
				if strings.HasPrefix(t, "&") {
					continue // a descriptor copy (2>&1), not a file
				}
				if t == "" && i+1 < len(words) {
					i++
					t = words[i]
				}
				add(t)
				continue
			}
			if strings.HasPrefix(a, "<<") {
				continue // a here-document marker; its body was read above
			}
			if strings.HasPrefix(a, "<") {
				t := strings.TrimLeft(a, "<")
				if t == "" && i+1 < len(words) {
					i++
					t = words[i]
				}
				if len(rest) > 0 && pathnorm.CommandBase(rest[0]) == "patch" {
					addPatch(t)
				}
				continue
			}
			rest = append(rest, words[i])
		}
		if len(rest) == 0 {
			continue
		}
		prog := pathnorm.CommandBase(rest[0])
		if m := interpreterVersionRe.FindStringSubmatch(prog); m != nil {
			prog = m[1]
		}
		switch interpreterKind(prog) {
		case "python":
			prog = "python"
		case "node":
			prog = "node"
		}
		args := rest[1:]
		switch prog {
		case "tee":
			for _, a := range operands(args, nil) {
				add(a)
			}
		case "sed":
			if inPlaceSed(args) {
				for _, a := range sedFiles(args) {
					add(a)
				}
			}
		case "perl", "ruby":
			if inPlaceScript(args) {
				for _, a := range scriptFiles(args) {
					add(a)
				}
			}
		case "cp", "install":
			copyTargets(args, add, false)
		case "mv":
			copyTargets(args, add, true)
		case "rm", "unlink", "shred":
			for _, a := range operands(args, nil) {
				add(a)
			}
		case "truncate":
			for _, a := range operands(args, map[string]bool{"-s": true, "--size": true, "-r": true, "--reference": true}) {
				add(a)
			}
		case "dd":
			for _, a := range args {
				if strings.HasPrefix(a, "of=") {
					add(strings.TrimPrefix(a, "of="))
				}
			}
		case "git":
			sub, gargs := gitSub(args)
			switch sub {
			case "checkout":
				for i, a := range gargs {
					if a == "--" {
						for _, p := range gargs[i+1:] {
							add(p)
						}
						break
					}
				}
			case "restore", "rm":
				for _, a := range operands(gargs, map[string]bool{"-s": true, "--source": true}) {
					add(a)
				}
			case "mv":
				for _, a := range operands(gargs, nil) {
					add(a)
				}
			case "apply":
				for _, a := range operands(gargs, map[string]bool{"-p": true, "--directory": true, "--exclude": true, "--include": true}) {
					addPatch(a)
				}
			}
		case "patch":
			files := operands(args, map[string]bool{"-p": true, "-d": true, "--directory": true, "-o": true, "--output": true, "-r": true, "-B": true, "-z": true, "-F": true, "-D": true})
			for i := 0; i < len(args); i++ {
				switch {
				case args[i] == "-i" || args[i] == "--input":
					if i+1 < len(args) {
						addPatch(args[i+1])
					}
				case strings.HasPrefix(args[i], "--input="):
					addPatch(strings.TrimPrefix(args[i], "--input="))
				}
			}
			if len(files) > 0 {
				add(files[0])
			}
			if len(files) > 1 {
				addPatch(files[1])
			}
		case "python":
			if code := optionValue(args, "-c"); code != "" {
				for _, p := range pythonWrites(code) {
					add(p)
				}
			}
		case "node":
			code := optionValue(args, "-e")
			if code == "" {
				code = optionValue(args, "--eval")
			}
			for _, m := range nodeWriteRe.FindAllStringSubmatch(code, -1) {
				add(m[1])
			}
		}
	}
	return w
}

var nodeWriteRe = lazyre.New(`\b(?:writeFileSync|appendFileSync|writeFile|appendFile|rmSync|unlinkSync|truncateSync)\(\s*['"]([^'"]+)['"]`)

// PatchTargets lists the files a unified or git diff changes, from its
// "---" and "+++" lines.
func PatchTargets(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "+++ ") && !strings.HasPrefix(line, "--- ") {
			continue
		}
		p := strings.TrimSpace(line[4:])
		if i := strings.IndexByte(p, '\t'); i >= 0 {
			p = p[:i]
		}
		p = strings.Trim(p, `"`)
		if p == "/dev/null" || p == "" {
			continue
		}
		if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
			p = p[2:]
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// dropPrefixes removes environment assignments and launchers that run the
// rest of the line unchanged.
func dropPrefixes(w []string) []string {
	for len(w) > 0 {
		head := w[0]
		switch {
		case envAssignRe.MatchString(head):
			w = w[1:]
		case head == "sudo" || head == "env" || head == "nohup" || head == "time" || head == "command" || head == "exec" || head == "(" || head == "{":
			w = w[1:]
			for len(w) > 0 && strings.HasPrefix(w[0], "-") {
				w = w[1:]
			}
		default:
			return w
		}
	}
	return w
}

var envAssignRe = lazyre.New(`^[A-Za-z_][A-Za-z0-9_]*=`)

// operands returns the words that are not options. A value option in
// valueOpts consumes the next word; "--" ends the options.
func operands(args []string, valueOpts map[string]bool) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return append(out, args[i+1:]...)
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if valueOpts[a] && i+1 < len(args) {
				i++
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

func optionValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"=")
		}
	}
	return ""
}

// inPlaceSed reports sed -i, -i.bak, --in-place or a short flag group that
// holds i (-Ei, -ni).
func inPlaceSed(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if strings.HasPrefix(a, "--in-place") {
			return true
		}
		if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(a[1:], 'i') {
			return true
		}
	}
	return false
}

// sedFiles returns the files of a sed command: the operands after the
// script, which is the first operand unless -e or -f gave it.
func sedFiles(args []string) []string {
	script := false
	var ops []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-e" || a == "-f" || a == "--expression" || a == "--file":
			script = true
			i++
		case strings.HasPrefix(a, "--expression=") || strings.HasPrefix(a, "--file="):
			script = true
		case a == "-l" || a == "--line-length":
			i++
		case strings.HasPrefix(a, "-") && len(a) > 1:
		default:
			ops = append(ops, a)
		}
	}
	if !script && len(ops) > 0 {
		ops = ops[1:]
	}
	return ops
}

// inPlaceScript reports perl or ruby run with -i in a short flag group.
func inPlaceScript(args []string) bool {
	for _, a := range args {
		if a == "--" || !strings.HasPrefix(a, "-") {
			return false
		}
		if a[1:] != "" && a[1] != '-' && strings.ContainsRune(a[1:], 'i') && !strings.HasPrefix(a, "-I") {
			return true
		}
	}
	return false
}

// scriptFiles returns the operands after a perl or ruby script.
func scriptFiles(args []string) []string {
	script := false
	var ops []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-e" || a == "-E":
			script = true
			i++
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if strings.HasSuffix(a, "e") && i+1 < len(args) {
				// -pie 'code': the e takes the next word as the script
				script = true
				i++
			}
		default:
			ops = append(ops, a)
		}
	}
	if !script && len(ops) > 0 {
		ops = ops[1:]
	}
	return ops
}

// copyTargets adds a copy's destination: the last operand, and the source
// names inside it when the destination is a folder. A move also changes its
// sources.
func copyTargets(args []string, add func(string), move bool) {
	dir := ""
	var ops []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-t" || a == "--target-directory":
			if i+1 < len(args) {
				dir = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--target-directory="):
			dir = strings.TrimPrefix(a, "--target-directory=")
		case a == "-S" || a == "--suffix" || a == "-m" || a == "--mode" || a == "-o" || a == "--owner" || a == "-g" || a == "--group":
			i++
		case a == "--":
			ops = append(ops, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-") && len(a) > 1:
		default:
			ops = append(ops, a)
		}
	}
	srcs := ops
	if dir == "" {
		if len(ops) < 2 {
			return
		}
		dir = ops[len(ops)-1]
		srcs = ops[:len(ops)-1]
		add(dir)
	}
	for _, s := range srcs {
		add(path.Join(dir, path.Base(s)))
		if move {
			add(s)
		}
	}
}
