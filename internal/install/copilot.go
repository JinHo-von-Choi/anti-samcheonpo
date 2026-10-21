package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
)

// CopilotEvents are the GitHub Copilot CLI hook events samcheonpo registers.
// The runtime names are the documented ones. The CLI also accepts the
// PascalCase aliases, which carry the VS Code compatible payload instead, so
// a hook file written for either spelling is read the same way.
var CopilotEvents = []string{
	"sessionStart",
	"sessionEnd",
	"userPromptSubmitted",
	"preToolUse",
	"postToolUse",
	"postToolUseFailure",
	"agentStop",
	"errorOccurred",
	"preCompact",
}

// CopilotTimeouts are the per event timeouts in seconds written to the hook
// file. Each one is longer than the transport wait, so a slow daemon never
// makes Copilot cut the hook off before the transport gives up.
var CopilotTimeouts = map[string]int{
	"sessionStart":        5,
	"sessionEnd":          5,
	"userPromptSubmitted": 5,
	"preToolUse":          5,
	"postToolUse":         5,
	"postToolUseFailure":  5,
	"agentStop":           160,
	"errorOccurred":       5,
	"preCompact":          10,
}

// CopilotUserHookPath is the user level hook file. A user level hook loads in
// every project, so it does not need the workspace to be trusted.
func CopilotUserHookPath() (string, error) {
	home := os.Getenv("COPILOT_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(h, ".copilot")
	}
	return filepath.Join(home, "hooks", "samcheonpo.json"), nil
}

// CopilotRepoHookPath is the repository level hook file. Copilot loads it only
// when the workspace is trusted.
func CopilotRepoHookPath(project string) string {
	return filepath.Join(project, ".github", "hooks", "samcheonpo.json")
}

// CopilotHookFile writes the Copilot CLI hook file. The command runs the
// transport directly, without a shell, so a path with spaces stays intact.
func CopilotHookFile(bin, path string, out func(string)) error {
	bin = hookclient.Executable(bin)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s가 이미 있다", path)
	}
	hooks := map[string]any{}
	for _, ev := range CopilotEvents {
		hooks[ev] = []map[string]any{{
			"type":       "command",
			"exec":       bin,
			"args":       []string{"hook", ev, "copilot"},
			"timeoutSec": CopilotTimeouts[ev],
		}}
	}
	b, _ := json.MarshalIndent(map[string]any{"version": 1, "hooks": hooks}, "", "  ")
	if err := installStandalone("copilot", path, append(b, '\n')); err != nil {
		return err
	}
	if out != nil {
		out("Copilot CLI 훅을 " + path + "에 썼다")
	}
	return nil
}

// CopilotFile writes the Copilot CLI repository hook file.
func CopilotFile(bin, project string, out func(string)) error {
	return CopilotHookFile(bin, CopilotRepoHookPath(project), out)
}

// CopilotInstallation describes one recorded Copilot hook file.
type CopilotInstallation struct {
	Path       string
	Scope      string // repository | user
	Events     int
	Missing    []string
	Executable bool
	Broken     string
}

// CopilotInstallations lists the recorded Copilot hook files.
func CopilotInstallations() []CopilotInstallation {
	paths, err := filepath.Glob(filepath.Join(config.Home(), "install-copilot-*.json"))
	if err != nil {
		return nil
	}
	var list []CopilotInstallation
	for _, state := range paths {
		raw, err := os.ReadFile(state)
		if err != nil {
			continue
		}
		var record standaloneRecord
		if json.Unmarshal(raw, &record) != nil || record.Path == "" {
			continue
		}
		item := CopilotInstallation{Path: record.Path, Scope: copilotScope(record.Path)}
		item.Events, item.Missing, item.Executable, item.Broken = inspectCopilotFile(record.Path)
		list = append(list, item)
	}
	return list
}

// copilotScope names where a hook file lives.
func copilotScope(path string) string {
	if strings.Contains(filepath.ToSlash(path), "/.github/hooks/") {
		return "repository"
	}
	return "user"
}

// inspectCopilotFile reads a hook file and reports what it does not state.
func inspectCopilotFile(path string) (int, []string, bool, string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, false, "훅 파일이 없거나 읽을 수 없습니다"
	}
	var root struct {
		Version int                         `json:"version"`
		Hooks   map[string][]map[string]any `json:"hooks"`
	}
	if json.Unmarshal(raw, &root) != nil {
		return 0, nil, false, "훅 파일 JSON 형식 오류"
	}
	if root.Version != 1 {
		return 0, nil, false, "훅 version은 1이어야 합니다"
	}
	var missing []string
	executable := false
	for _, name := range CopilotEvents {
		entries := root.Hooks[name]
		if len(entries) == 0 {
			missing = append(missing, name)
			continue
		}
		for _, entry := range entries {
			exec, _ := entry["exec"].(string)
			if exec == "" {
				continue
			}
			if !executable {
				if info, err := os.Stat(exec); err == nil {
					executable = info.Mode().IsRegular() && (runtime.GOOS == "windows" || info.Mode().Perm()&0111 != 0)
				}
			}
			args, _ := entry["args"].([]any)
			if len(args) != 3 || args[0] != "hook" || args[2] != "copilot" {
				missing = append(missing, name+"(args)")
			}
		}
	}
	return len(root.Hooks), missing, executable, ""
}

// RemoveCopilotHooks removes every recorded Copilot hook file.
func RemoveCopilotHooks(out func(string)) error {
	items := CopilotInstallations()
	if len(items) == 0 {
		return errors.New("Copilot 설치 기록이 없습니다")
	}
	for _, item := range items {
		if err := removeStandalone("copilot", item.Path); err != nil {
			return err
		}
		if out != nil {
			out(item.Path + "를 지웠다")
		}
	}
	return nil
}
