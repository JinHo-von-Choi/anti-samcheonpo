package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallUninstallRestoresSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(home, ".samcheonpo"))
	settings := filepath.Join(home, "settings.json")
	orig := "{\n  \"model\": \"opus\",\n  \"statusLine\": {\"type\": \"command\", \"command\": \"echo mine\"},\n  \"hooks\": {\"Stop\": []}\n}\n"
	if err := os.WriteFile(settings, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Install(Options{Binary: "/opt/bin/samcheonpo", SettingsPath: settings}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(settings)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	var sl struct{ Command string }
	_ = json.Unmarshal(m["statusLine"], &sl)
	if !strings.Contains(sl.Command, "statusline --wrap 'echo mine'") {
		t.Fatalf("existing status line must be wrapped: %s", sl.Command)
	}
	if string(m["model"]) != `"opus"` || m["hooks"] == nil {
		t.Error("other settings must be preserved")
	}
	hooks, err := os.ReadFile(filepath.Join(home, ".samcheonpo", "plugin", "claude-code", "hooks", "hooks.json"))
	if err != nil || !strings.Contains(string(hooks), `\"/opt/bin/samcheonpo\" hook PreToolUse`) {
		t.Fatalf("plugin hooks must call the installed binary: %s %v", hooks, err)
	}
	var hj map[string]any
	if err := json.Unmarshal(hooks, &hj); err != nil {
		t.Fatalf("hooks.json must stay valid JSON: %v", err)
	}
	if err := Install(Options{Binary: "/x", SettingsPath: settings}); err == nil {
		t.Error("a second install must refuse")
	}
	if err := Uninstall(nil); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(settings)
	var after, before map[string]any
	_ = json.Unmarshal(b, &after)
	_ = json.Unmarshal([]byte(orig), &before)
	ja, _ := json.Marshal(after)
	jb, _ := json.Marshal(before)
	if string(ja) != string(jb) {
		t.Errorf("settings after uninstall differ from before install:\n%s\n%s", ja, jb)
	}
	if _, err := os.Stat(filepath.Join(home, ".samcheonpo", "plugin")); !os.IsNotExist(err) {
		t.Error("plugin directory must be removed")
	}
}

func TestUninstallKeepsUserChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(home, ".samcheonpo"))
	settings := filepath.Join(home, "settings.json")
	if err := Install(Options{Binary: "/opt/bin/samcheonpo", SettingsPath: settings}); err != nil {
		t.Fatal(err)
	}
	user := `{"statusLine": {"type": "command", "command": "my-new-line"}}`
	_ = os.WriteFile(settings, []byte(user), 0o600)
	var msgs []string
	if err := Uninstall(func(s string) { msgs = append(msgs, s) }); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(settings)
	if !strings.Contains(string(b), "my-new-line") || len(msgs) == 0 {
		t.Errorf("a status line changed by the user must stay: %s %v", b, msgs)
	}
}

func TestInstallWithoutSettingsFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(home, ".samcheonpo"))
	settings := filepath.Join(home, "claude", "settings.json")
	if err := Install(Options{Binary: "/opt/b", SettingsPath: settings}); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Errorf("a settings file created by install is removed by uninstall: %v", err)
	}
}

func TestCodexHooksMergeAndRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(home, ".samcheonpo"))
	p := filepath.Join(home, "hooks.json")
	orig := `{"description":"mine","hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"python3 mine.py"}]}]}}`
	_ = os.WriteFile(p, []byte(orig), 0o600)
	if err := InstallHooks(Codex, "/opt/samcheonpo", p, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "mine.py") || !strings.Contains(string(b), `\"/opt/samcheonpo\" hook PreToolUse codex`) {
		t.Fatalf("merge keeps user hooks and adds ours: %s", b)
	}
	if err := UninstallHooks("codex", nil); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	var after, before any
	_ = json.Unmarshal(b, &after)
	_ = json.Unmarshal([]byte(orig), &before)
	ja, _ := json.Marshal(after)
	jb, _ := json.Marshal(before)
	if string(ja) != string(jb) {
		t.Errorf("uninstall restores the file:\n%s\n%s", ja, jb)
	}
}

func TestCursorAndCopilotFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(home, ".samcheonpo"))
	cp := filepath.Join(home, "cursor.json")
	_ = os.WriteFile(cp, []byte(`{"version":1,"hooks":{"stop":[{"command":"./mine.sh"}]}}`), 0o600)
	if err := InstallCursor("/opt/s", cp, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cp)
	if !strings.Contains(string(b), "mine.sh") || !strings.Contains(string(b), "beforeShellExecution cursor") {
		t.Fatalf("cursor merge: %s", b)
	}
	if err := UninstallCursor(nil); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(cp)
	if strings.Contains(string(b), "/opt/s") || !strings.Contains(string(b), "mine.sh") {
		t.Fatalf("cursor uninstall: %s", b)
	}
	proj := filepath.Join(home, "proj")
	if err := CopilotFile("/opt/s", proj, nil); err != nil {
		t.Fatal(err)
	}
	cb, _ := os.ReadFile(filepath.Join(proj, ".github", "hooks", "samcheonpo.json"))
	var m map[string]any
	if json.Unmarshal(cb, &m) != nil || m["version"].(float64) != 1 || !strings.Contains(string(cb), "PreToolUse copilot") {
		t.Fatalf("copilot file: %s", cb)
	}
	if err := RemoveCopilotFile(proj, nil); err != nil {
		t.Fatal(err)
	}
}
