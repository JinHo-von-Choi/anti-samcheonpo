//go:build windows

package procgroup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

var (
	shellOnce sync.Once
	shellPath string
)

// gitBash locates the bash that ships with Git for Windows. It never returns
// the WSL launcher in System32, which would run commands inside a Linux
// distribution instead of on this machine. SAMCHEONPO_SHELL overrides the
// search with an explicit executable.
func gitBash() string {
	shellOnce.Do(func() {
		if p := os.Getenv("SAMCHEONPO_SHELL"); p != "" {
			shellPath = p
			return
		}
		var cands []string
		if git, err := exec.LookPath("git"); err == nil {
			// <root>\cmd\git.exe, <root>\bin\git.exe or <root>\mingw64\bin\git.exe
			dir := filepath.Dir(git)
			for _, up := range []string{"..", filepath.Join("..", "..")} {
				cands = append(cands, filepath.Join(dir, up, "bin", "bash.exe"))
			}
		}
		for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			base := os.Getenv(env)
			if base == "" {
				continue
			}
			if env == "LOCALAPPDATA" {
				base = filepath.Join(base, "Programs")
			}
			cands = append(cands, filepath.Join(base, "Git", "bin", "bash.exe"))
		}
		sys := strings.ToLower(os.Getenv("SystemRoot"))
		for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
			if dir == "" || (sys != "" && strings.HasPrefix(strings.ToLower(dir), sys)) {
				continue
			}
			cands = append(cands, filepath.Join(dir, "bash.exe"))
		}
		for _, c := range cands {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				shellPath = filepath.Clean(c)
				return
			}
		}
	})
	return shellPath
}

// ShellName names the interpreter Shell uses: "bash" when Git for Windows is
// installed, otherwise "cmd".
func ShellName() string {
	if gitBash() != "" {
		return "bash"
	}
	return "cmd"
}

// Shell runs command through Git for Windows' bash so that a command means the
// same thing here as on Unix. Without it the command goes to the command
// interpreter (%COMSPEC%) with its own quoting rules.
func Shell(command string) *exec.Cmd {
	if bash := gitBash(); bash != "" {
		// bash.exe splits its command line with MSYS rules. Go quotes an
		// argument only when it contains a space or tab, and an unquoted
		// argument holding \" is read by MSYS as the start of a quoted span.
		// The trailing space forces the quoting and is invisible to bash.
		return exec.Command(bash, "-c", command+" ")
	}
	return cmdShell(command)
}

// cmdShell runs command through the command interpreter (%COMSPEC%).
func cmdShell(command string) *exec.Cmd {
	sh := os.Getenv("COMSPEC")
	if sh == "" {
		sh = "cmd.exe"
	}
	// cmd.exe does not read the \" escaping that exec builds, so the command
	// line is composed by hand: /S makes it strip exactly one outer pair of
	// quotes and keep the rest as written.
	cmd := exec.Command(sh)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + sh + `" /S /C "` + command + `"`}
	return cmd
}
