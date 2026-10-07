package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
)

// HookDialect describes a Claude-family hooks.json file of another agent.
type HookDialect struct {
	Agent  string
	Events map[string]int // event -> timeout seconds
}

// Codex is the Codex CLI hooks.json dialect (tested with 0.160.0).
var Codex = HookDialect{Agent: "codex", Events: map[string]int{
	"SessionStart": 5, "UserPromptSubmit": 5, "PreToolUse": 5, "PostToolUse": 5, "Stop": 160, "PreCompact": 5, "SessionEnd": 3,
}}

type hookState struct {
	Agent   string                     `json:"agent"`
	Path    string                     `json:"path"`
	Existed bool                       `json:"existed"`
	Marker  string                     `json:"marker"`
	At      time.Time                  `json:"installed_at"`
	Entries map[string]json.RawMessage `json:"entries,omitempty"`
}

func hookStatePath(agent string) string {
	return filepath.Join(config.Home(), "install-state-"+agent+".json")
}

// marker identifies the commands samcheonpo added: "<bin> hook <Event> <agent>".
func marker(bin, agent string) string { return strconv.Quote(bin) + " hook " }

// InstallHooks merges samcheonpo entries into an agent's hooks.json without
// touching existing entries.
func InstallHooks(d HookDialect, bin, path string, out func(string)) error {
	bin = hookclient.Executable(bin)
	if out == nil {
		out = func(string) {}
	}
	if _, err := os.Lstat(hookStatePath(d.Agent)); err == nil {
		return fmt.Errorf("%s 연결이 이미 설치되어 있다", d.Agent)
	} else if !os.IsNotExist(err) {
		return err
	}
	root, raw, err := readSettings(path)
	if err != nil {
		return err
	}
	existed := raw != nil
	hooks := map[string][]json.RawMessage{}
	if h, ok := root["hooks"]; ok {
		if err := json.Unmarshal(h, &hooks); err != nil {
			return fmt.Errorf("%s의 hooks를 해석하지 못했다: %w", path, err)
		}
		if hooks == nil {
			return errors.New("hooks는 JSON 객체여야 합니다")
		}
	}
	if err := backup(path, raw); err != nil {
		return err
	}
	mk := marker(bin, d.Agent)
	installed := map[string]json.RawMessage{}
	for ev, timeout := range d.Events {
		entry, _ := json.Marshal(map[string]any{
			"matcher": "*",
			"hooks":   []map[string]any{{"type": "command", "command": mk + ev + " " + d.Agent, "timeout": timeout}},
		})
		hooks[ev] = append(hooks[ev], entry)
		installed[ev] = entry
	}
	hb, _ := json.Marshal(hooks)
	root["hooks"] = hb
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	st, _ := json.MarshalIndent(hookState{Agent: d.Agent, Path: path, Existed: existed, Marker: mk, At: time.Now().UTC(), Entries: installed}, "", "  ")
	if err := writeHookSettings(path, append(b, '\n'), raw, hookStatePath(d.Agent), st); err != nil {
		return err
	}
	out(fmt.Sprintf("%s 훅을 %s에 추가했다. 에이전트의 훅 신뢰 확인(/hooks)이 필요할 수 있다", d.Agent, path))
	return nil
}

// UninstallHooks removes exactly the entries InstallHooks added.
func UninstallHooks(agent string, out func(string)) error {
	if out == nil {
		out = func(string) {}
	}
	sb, err := os.ReadFile(hookStatePath(agent))
	if err != nil {
		return errors.New(agent + " 설치 기록이 없다")
	}
	var st hookState
	if err := json.Unmarshal(sb, &st); err != nil {
		return err
	}
	root, raw, err := readSettings(st.Path)
	if err != nil {
		return err
	}
	if err == nil {
		hooks := map[string][]json.RawMessage{}
		if h, ok := root["hooks"]; ok {
			if err := json.Unmarshal(h, &hooks); err != nil || hooks == nil {
				return errors.New("hooks는 이벤트 배열을 담은 JSON 객체여야 합니다")
			}
		}
		removed := 0
		for ev, entries := range hooks {
			var keep []json.RawMessage
			for _, e := range entries {
				if (st.Entries != nil && jsonEqual(e, st.Entries[ev])) || (st.Entries == nil && legacyHookEntry(e, st.Marker, ev, agent)) {
					removed++
					continue
				}
				keep = append(keep, e)
			}
			if len(keep) == 0 {
				delete(hooks, ev)
			} else {
				hooks[ev] = keep
			}
		}
		if len(hooks) == 0 {
			delete(root, "hooks")
		} else {
			hb, _ := json.Marshal(hooks)
			root["hooks"] = hb
		}
		if err := backup(st.Path, raw); err != nil {
			return err
		}
		if len(root) == 0 && !st.Existed {
			if err := os.Remove(st.Path); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else {
			b, _ := json.MarshalIndent(root, "", "  ")
			if err := writeAtomic(st.Path, append(b, '\n'), 0o600); err != nil {
				return err
			}
		}
		out(fmt.Sprintf("%s에서 삼천포 훅 %d개를 뺐다", st.Path, removed))
	}
	return os.Remove(hookStatePath(agent))
}

func jsonInner(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// AgyGroup is the hook group name samcheonpo owns in agy's hooks.json.
const AgyGroup = "samcheonpo"

// agyGroup builds the agy 1.3 hook group: tool events take matcher entries,
// invocation and stop events take plain handlers.
func agyGroup(bin string) json.RawMessage {
	cmd := func(ev string) map[string]any {
		timeout := 5
		if ev == "Stop" {
			timeout = 160
		}
		return map[string]any{"type": "command", "command": marker(bin, "agy") + ev + " agy", "timeout": timeout}
	}
	g := map[string]any{
		"PreToolUse":    []map[string]any{{"matcher": "*", "hooks": []map[string]any{cmd("PreToolUse")}}},
		"PostToolUse":   []map[string]any{{"matcher": "*", "hooks": []map[string]any{cmd("PostToolUse")}}},
		"PreInvocation": []map[string]any{cmd("PreInvocation")},
		"Stop":          []map[string]any{cmd("Stop")},
	}
	b, _ := json.Marshal(g)
	return b
}

// InstallAgy adds the samcheonpo group to agy's global hooks.json
// (~/.gemini/config/hooks.json), keeping every other group.
func InstallAgy(bin, path string, out func(string)) error {
	bin = hookclient.Executable(bin)
	if out == nil {
		out = func(string) {}
	}
	if _, err := os.Lstat(hookStatePath("agy")); err == nil {
		return errors.New("agy 연결이 이미 설치되어 있다")
	} else if !os.IsNotExist(err) {
		return err
	}
	root, raw, err := readSettings(path)
	if err != nil {
		return err
	}
	if _, ok := root[AgyGroup]; ok {
		return fmt.Errorf("%s에 %q 훅 묶음이 이미 있다", path, AgyGroup)
	}
	if err := backup(path, raw); err != nil {
		return err
	}
	g := agyGroup(bin)
	root[AgyGroup] = g
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	st, _ := json.MarshalIndent(hookState{Agent: "agy", Path: path, Existed: raw != nil, Marker: marker(bin, "agy"), At: time.Now().UTC(), Entries: map[string]json.RawMessage{AgyGroup: g}}, "", "  ")
	if err := writeHookSettings(path, append(b, '\n'), raw, hookStatePath("agy"), st); err != nil {
		return err
	}
	out(fmt.Sprintf("agy 훅 묶음 %q를 %s에 추가했다", AgyGroup, path))
	return nil
}

// UninstallAgy removes the group InstallAgy added, if it is unchanged.
// Records from older versions (event entries in a project hooks.json) are
// removed the generic way.
func UninstallAgy(out func(string)) error {
	if out == nil {
		out = func(string) {}
	}
	sb, err := os.ReadFile(hookStatePath("agy"))
	if err != nil {
		return errors.New("agy 설치 기록이 없다")
	}
	var st hookState
	if err := json.Unmarshal(sb, &st); err != nil {
		return err
	}
	want, ok := st.Entries[AgyGroup]
	if !ok {
		return UninstallHooks("agy", out)
	}
	root, raw, err := readSettings(st.Path)
	if err != nil {
		return err
	}
	if cur, ok := root[AgyGroup]; ok {
		if !jsonEqual(cur, want) {
			return fmt.Errorf("%s의 %q 훅 묶음이 설치 뒤 바뀌어 지우지 않았다. 직접 확인한 뒤 지우고 %s를 삭제하면 된다", st.Path, AgyGroup, hookStatePath("agy"))
		}
		delete(root, AgyGroup)
		if err := backup(st.Path, raw); err != nil {
			return err
		}
		if len(root) == 0 && !st.Existed {
			if err := os.Remove(st.Path); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else {
			b, _ := json.MarshalIndent(root, "", "  ")
			if err := writeAtomic(st.Path, append(b, '\n'), 0o600); err != nil {
				return err
			}
		}
		out(fmt.Sprintf("%s에서 %q 훅 묶음을 뺐다", st.Path, AgyGroup))
	}
	return os.Remove(hookStatePath("agy"))
}

// CopilotFile writes the Copilot CLI repository hook file (PascalCase mode).
func CopilotFile(bin, project string, out func(string)) error {
	bin = hookclient.Executable(bin)
	p := filepath.Join(project, ".github", "hooks", "samcheonpo.json")
	if _, err := os.Stat(p); err == nil {
		return fmt.Errorf("%s가 이미 있다", p)
	}
	hooks := map[string]any{}
	for ev, t := range map[string]int{"SessionStart": 5, "PreToolUse": 5, "PostToolUse": 5, "Stop": 160} {
		hooks[ev] = []map[string]any{{"type": "command", "command": strconv.Quote(bin) + " hook " + ev + " copilot", "timeoutSec": t}}
	}
	b, _ := json.MarshalIndent(map[string]any{"version": 1, "hooks": hooks}, "", "  ")
	if err := installStandalone("copilot", p, append(b, '\n')); err != nil {
		return err
	}
	if out != nil {
		out("Copilot CLI 훅을 " + p + "에 썼다")
	}
	return nil
}

// RemoveCopilotFile removes the Copilot hook file written by CopilotFile.
func RemoveCopilotFile(project string, out func(string)) error {
	p := filepath.Join(project, ".github", "hooks", "samcheonpo.json")
	if err := removeStandalone("copilot", p); err != nil {
		return err
	}
	if out != nil {
		out(p + "를 지웠다")
	}
	return nil
}

// CursorEvents are the Cursor hook events samcheonpo registers.
var CursorEvents = []string{"sessionStart", "sessionEnd", "beforeSubmitPrompt", "beforeShellExecution", "afterShellExecution", "beforeReadFile", "afterFileEdit", "stop", "preCompact"}

// InstallCursor merges samcheonpo commands into a Cursor hooks.json.
func InstallCursor(bin, path string, out func(string)) error {
	bin = hookclient.Executable(bin)
	if _, err := os.Lstat(hookStatePath("cursor")); err == nil {
		return errors.New("cursor 연결이 이미 설치되어 있다")
	} else if !os.IsNotExist(err) {
		return err
	}
	root, raw, err := readSettings(path)
	if err != nil {
		return err
	}
	existed := raw != nil
	if _, ok := root["version"]; !ok {
		root["version"] = json.RawMessage(`1`)
	}
	hooks := map[string][]json.RawMessage{}
	if h, ok := root["hooks"]; ok {
		if err := json.Unmarshal(h, &hooks); err != nil || hooks == nil {
			return errors.New("hooks는 이벤트 배열을 담은 JSON 객체여야 합니다")
		}
	}
	if err := backup(path, raw); err != nil {
		return err
	}
	mk := marker(bin, "cursor")
	installed := map[string]json.RawMessage{}
	for _, ev := range CursorEvents {
		entry, _ := json.Marshal(map[string]any{"command": mk + ev + " cursor"})
		hooks[ev] = append(hooks[ev], entry)
		installed[ev] = entry
	}
	root["hooks"], _ = json.Marshal(hooks)
	b, _ := json.MarshalIndent(root, "", "  ")
	st, _ := json.MarshalIndent(hookState{Agent: "cursor", Path: path, Existed: existed, Marker: mk, At: time.Now().UTC(), Entries: installed}, "", "  ")
	if err := writeHookSettings(path, append(b, '\n'), raw, hookStatePath("cursor"), st); err != nil {
		return err
	}
	if out != nil {
		out("Cursor 훅을 " + path + "에 추가했다 (관찰 전용: 실제 Cursor 기록으로 확인 전까지 개입하지 않는다)")
	}
	return nil
}

// UninstallCursor removes the entries InstallCursor added.
func UninstallCursor(out func(string)) error {
	return UninstallHooks("cursor", out)
}

func writeHookSettings(path string, next, previous []byte, state string, record []byte) error {
	if err := os.MkdirAll(filepath.Dir(state), 0700); err != nil {
		return err
	}
	if err := writeAtomic(path, next, 0600); err != nil {
		return err
	}
	if err := writeAtomic(state, record, 0600); err != nil {
		var restore error
		if previous == nil {
			restore = os.Remove(path)
		} else {
			restore = writeAtomic(path, previous, 0600)
		}
		return errors.Join(err, restore)
	}
	return nil
}

func legacyHookEntry(entry json.RawMessage, prefix, event, agent string) bool {
	if prefix == "" {
		return false
	}
	command := prefix + event + " " + agent
	var v map[string]json.RawMessage
	if json.Unmarshal(entry, &v) != nil {
		return false
	}
	if agent == "cursor" {
		var c string
		return len(v) == 1 && json.Unmarshal(v["command"], &c) == nil && c == command
	}
	if len(v) != 2 || string(v["matcher"]) != `"*"` {
		return false
	}
	var hooks []map[string]json.RawMessage
	if json.Unmarshal(v["hooks"], &hooks) != nil || len(hooks) != 1 || len(hooks[0]) != 3 {
		return false
	}
	var c string
	return string(hooks[0]["type"]) == `"command"` && json.Unmarshal(hooks[0]["command"], &c) == nil && c == command
}
