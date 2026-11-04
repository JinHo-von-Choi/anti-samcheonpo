package procscan

import (
	"path/filepath"
	"strings"
)

// InfrastructureRole recognizes installed host executables, never a project
// program merely named codex. The monitor and its hook are known sibling
// executables. These are not workspace servers the session forgot to stop.
func InfrastructureRole(exe, monitorExe string) string {
	exe = filepath.Clean(exe)
	if monitorExe != "" {
		monitorExe = filepath.Clean(monitorExe)
		if exe == monitorExe {
			return "monitor"
		}
		if exe == filepath.Join(filepath.Dir(monitorExe), "samcheonpo-hook") {
			return "monitor_hook"
		}
	}
	if filepath.Base(exe) == "codex" && (strings.Contains(filepath.ToSlash(exe), "/@openai/codex/") || strings.Contains(filepath.ToSlash(exe), "/@openai/codex-linux-")) {
		return "codex_host"
	}
	return ""
}
