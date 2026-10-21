package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCursorUpgradePreservesCustomHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	path := filepath.Join(home, "custom-hooks.json")
	old := json.RawMessage(`{"command":"\"/old/s\" hook beforeShellExecution cursor"}`)
	modified := json.RawMessage(`{"command":"\"/old/s\" hook stop cursor","timeout":12}`)
	custom := json.RawMessage(`{"command":"./other","matcher":"Shell","timeout":9}`)
	root := map[string]any{"version": 1, "custom": "keep", "hooks": map[string][]json.RawMessage{"beforeShellExecution": {old}, "stop": {modified}, "preToolUse": {custom}}}
	raw, _ := json.Marshal(root)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	state := hookState{Agent: "cursor", Path: path, Existed: true, Marker: marker("/old/s", "cursor"), Entries: map[string]json.RawMessage{"beforeShellExecution": old, "stop": json.RawMessage(`{"command":"\"/old/s\" hook stop cursor"}`)}}
	raw, _ = json.Marshal(state)
	if err := os.WriteFile(hookStatePath("cursor"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := InstallCursor("/new/s", path, nil); err != nil {
			t.Fatal(err)
		}
	}
	m, _, err := readSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	var hooks map[string][]json.RawMessage
	_ = json.Unmarshal(m["hooks"], &hooks)
	if len(hooks["beforeShellExecution"]) != 0 || len(hooks["preToolUse"]) != 2 || len(hooks["stop"]) != 2 {
		t.Fatalf("upgrade %s", m["hooks"])
	}
	if !jsonEqual(hooks["preToolUse"][0], custom) || !jsonEqual(hooks["stop"][0], modified) {
		t.Fatal("custom entry changed")
	}
	if err := UninstallCursor(nil); err != nil {
		t.Fatal(err)
	}
	m, _, _ = readSettings(path)
	_ = json.Unmarshal(m["hooks"], &hooks)
	if string(m["custom"]) != `"keep"` || len(hooks["preToolUse"]) != 1 || len(hooks["stop"]) != 1 {
		t.Fatal("uninstall removed custom settings")
	}
}

func TestCursorInspectAndPathConflict(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	if state, err := InspectCursor(); err != nil || state.Installed {
		t.Fatal(state, err)
	}
	path := filepath.Join(home, "hooks.json")
	bin := filepath.Join(home, "samcheonpo")
	if err := os.WriteFile(bin, []byte("binary"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := InstallCursor(bin, path, nil); err != nil {
		t.Fatal(err)
	}
	if state, err := InspectCursor(); err != nil || !state.Installed || !state.Executable || len(state.Missing) > 0 {
		t.Fatal(state, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(bin, 0600); err != nil {
			t.Fatal(err)
		}
		if state, err := InspectCursor(); err != nil || state.Executable {
			t.Fatal("non-executable hook accepted", state, err)
		}
	}
	if err := InstallCursor(bin, filepath.Join(home, "other.json"), nil); err == nil {
		t.Fatal("silently moved installation")
	}
	root, _, _ := readSettings(path)
	var hooks map[string][]json.RawMessage
	_ = json.Unmarshal(root["hooks"], &hooks)
	delete(hooks, "postToolUseFailure")
	root["hooks"], _ = json.Marshal(hooks)
	raw, _ := json.Marshal(root)
	os.WriteFile(path, raw, 0600)
	if state, err := InspectCursor(); err != nil || len(state.Missing) != 1 || state.Missing[0] != "postToolUseFailure" {
		t.Fatal(state, err)
	}
}

func TestCursorInvalidVersionPreservesSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	path := filepath.Join(home, "hooks.json")
	original := `{"version":2,"hooks":{},"custom":"keep"}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallCursor("/opt/s", path, nil); err == nil {
		t.Fatal("unsupported hook schema accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatal("failed install changed user settings")
	}
	if _, err := os.Stat(hookStatePath("cursor")); !os.IsNotExist(err) {
		t.Fatal("failed install created state")
	}
}
