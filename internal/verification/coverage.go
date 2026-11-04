package verification

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
)

// CoversPathAt resolves the target in the command's actual working directory.
// A recursive suite in an unrelated subdirectory cannot cover the project.
func CoversPathAt(command, changed, root, dir string) bool {
	if root == "" {
		root = "."
	}
	if !pathnorm.IsAbs(changed) {
		changed = filepath.Join(root, changed)
	}
	if dir == "" {
		dir = root
	} else if !pathnorm.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	rel, err := filepath.Rel(dir, changed)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return CoversPath(command, filepath.ToSlash(rel))
}

// CoversPath is a conservative relationship, not proof of test quality. Unknown
// scripts, shell composition, package aliases and selectors require explicit
// accepted checks. A path merely appearing in echo/cat is not verification.
func CoversPath(command, changed string) bool {
	if strings.ContainsAny(command, ";|&`\n<>") || strings.Contains(command, "$(") {
		return false
	}
	f := strings.Fields(command)
	for len(f) > 0 && strings.Contains(f[0], "=") {
		f = f[1:]
	}
	if len(f) < 1 {
		return false
	}
	program := path.Base(f[0])
	args := f[1:]
	for _, arg := range args {
		switch strings.SplitN(arg, "=", 2)[0] {
		case "-list", "--collect-only", "--list", "--no-run", "--help", "-h":
			return false
		}
	}
	p := strings.TrimPrefix(path.Clean(changed), "./")
	dir := path.Dir(p)
	switch program {
	case "go":
		if len(args) < 1 || args[0] != "test" && args[0] != "vet" && args[0] != "build" {
			return false
		}
		args = args[1:]
		targets := []string{}
		for i := 0; i < len(args); i++ {
			a := args[i]
			if strings.HasPrefix(a, "-") {
				if !strings.Contains(a, "=") {
					switch a {
					case "-run", "-skip", "-count", "-timeout", "-tags", "-parallel", "-p", "-coverprofile", "-coverpkg", "-bench", "-benchtime", "-o":
						i++
					}
				}
				continue
			}
			targets = append(targets, strings.TrimPrefix(a, "./"))
		}
		if len(targets) == 0 {
			targets = []string{"."}
		}
		for _, t := range targets {
			if t == "..." {
				return strings.HasSuffix(p, ".go") || path.Base(p) == "go.mod" || path.Base(p) == "go.sum"
			}
			if strings.HasSuffix(t, "/...") {
				base := strings.TrimSuffix(t, "/...")
				if dir == base || strings.HasPrefix(dir, base+"/") {
					return true
				}
			}
			if t == dir || t == p {
				return true
			}
		}
	case "pytest", "pytest-3":
		if !strings.HasSuffix(p, ".py") {
			return false
		}
		targets := []string{}
		for i := 0; i < len(args); i++ {
			a := args[i]
			if strings.HasPrefix(a, "-") {
				if a == "-k" || a == "-m" || a == "-c" {
					i++
				}
				continue
			}
			targets = append(targets, strings.TrimPrefix(strings.Split(a, "::")[0], "./"))
		}
		if len(targets) == 0 {
			return true
		}
		for _, t := range targets {
			if t == p || t == dir || t == "." || strings.HasPrefix(p, t+"/") {
				return true
			}
		}
	case "python", "python3", "node", "ruby":
		if len(args) >= 2 && args[0] == "-m" && args[1] == "unittest" {
			if len(args) == 2 || len(args) == 3 && (args[2] == "discover" || args[2] == "-v" || args[2] == "-q") {
				return strings.HasSuffix(p, ".py")
			}
		}
		if len(args) > 1 && args[0] == "-m" && args[1] == "pytest" {
			return CoversPath("pytest "+strings.Join(args[2:], " "), p)
		}
		if len(args) > 0 && strings.TrimPrefix(args[0], "./") == p {
			return true
		}
	case "cargo":
		if len(args) > 0 && (args[0] == "test" || args[0] == "check" || args[0] == "build") && !strings.Contains(command, " -p ") && !strings.Contains(command, "--package") {
			return strings.HasSuffix(p, ".rs") || path.Base(p) == "Cargo.toml" || path.Base(p) == "Cargo.lock"
		}
	case "npm", "pnpm", "yarn", "bun":
		// Root test scripts are project-wide candidates; workspace and selected
		// file invocations cannot stand in for another package's checks.
		if len(args) > 0 && args[0] == "run" {
			args = args[1:]
		}
		if len(args) == 1 && (args[0] == "test" || args[0] == "check" || args[0] == "build" || args[0] == "typecheck") {
			ext := path.Ext(p)
			return ext == ".js" || ext == ".ts" || ext == ".tsx" || ext == ".jsx" || ext == ".vue" || ext == ".svelte" || path.Base(p) == "package.json"
		}
	}
	return false
}
