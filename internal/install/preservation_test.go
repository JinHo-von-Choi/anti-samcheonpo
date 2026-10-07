package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallRejectsInvalidSettingsWithoutMutation(t *testing.T) {
	for _, body := range []string{`null`, `{`, `[]`} {
		for _, agent := range []string{"claude", "codex", "cursor"} {
			t.Run(agent+body, func(t *testing.T) {
				dir := t.TempDir()
				home := filepath.Join(dir, "home")
				t.Setenv("SAMCHEONPO_HOME", home)
				path := filepath.Join(dir, "settings.json")
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				switch agent {
				case "claude":
					err = Install(Options{Binary: "/bin/s", SettingsPath: path})
				case "codex":
					err = InstallHooks(Codex, "/bin/s", path, nil)
				case "cursor":
					err = InstallCursor("/bin/s", path, nil)
				}
				if err == nil {
					t.Fatal("invalid settings accepted")
				}
				b, _ := os.ReadFile(path)
				if string(b) != body {
					t.Fatal("settings overwritten")
				}
				if _, err := os.Stat(home); !os.IsNotExist(err) {
					t.Fatal("created installation before validation")
				}
			})
		}
	}
}

func TestInstallPreservesExistingPluginAndTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", dir)
	root := filepath.Join(dir, "plugin")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "mine")
	_ = os.WriteFile(sentinel, []byte("mine"), 0600)
	if err := Install(Options{Binary: "/bin/s", SettingsPath: filepath.Join(dir, "settings.json")}); err == nil {
		t.Fatal("existing plugin overwritten")
	}
	if b, _ := os.ReadFile(sentinel); string(b) != "mine" {
		t.Fatal("plugin destroyed")
	}
	path := filepath.Join(dir, "atomic")
	_ = os.WriteFile(path+".samcheonpo.tmp", []byte("mine"), 0600)
	if err := writeAtomic(path, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path + ".samcheonpo.tmp"); string(b) != "mine" {
		t.Fatal("unrelated temp destroyed")
	}
}

func TestUninstallPreservesModifiedAndAddedPluginFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", dir)
	if err := Install(Options{Binary: "/bin/s", SettingsPath: filepath.Join(dir, "settings.json")}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "plugin")
	modified := filepath.Join(root, "claude-code", "hooks", "hooks.json")
	added := filepath.Join(root, "mine")
	for _, path := range []string{modified, added} {
		if err := os.WriteFile(path, []byte("user change"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Uninstall(nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{modified, added} {
		if b, _ := os.ReadFile(path); string(b) != "user change" {
			t.Fatalf("lost %s", path)
		}
	}
}

func TestGeneratedCleanupDoesNotFollowReplacedParent(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "plugin")
	child := filepath.Join(root, "sub")
	_ = os.MkdirAll(child, 0700)
	_ = os.WriteFile(filepath.Join(child, "file"), []byte("same"), 0600)
	manifest, err := generatedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "outside")
	if err := os.Rename(child, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, child); err != nil {
		t.Fatal(err)
	}
	if err := removeGenerated(root, manifest, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "file")); string(b) != "same" {
		t.Fatal("followed symlink")
	}
}

func TestHookUninstallPreservesUserAddedEntryAndRootKey(t *testing.T) {
	for _, agent := range []string{"codex", "cursor"} {
		t.Run(agent, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("SAMCHEONPO_HOME", dir)
			path := filepath.Join(dir, "hooks.json")
			var err error
			if agent == "cursor" {
				err = InstallCursor("/bin/s", path, nil)
			} else {
				err = InstallHooks(Codex, "/bin/s", path, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			root, _, _ := readSettings(path)
			root["user"] = json.RawMessage(`"keep"`)
			var hooks map[string][]json.RawMessage
			_ = json.Unmarshal(root["hooks"], &hooks)
			for event, list := range hooks {
				// A user's entry merely mentions our marker; it is not our installed entry.
				hooks[event] = append(list, json.RawMessage(`{"command":"echo user","note":"\\\"/bin/s\\\" hook "}`))
				break
			}
			root["hooks"], _ = json.Marshal(hooks)
			if err := writeSettings(path, root); err != nil {
				t.Fatal(err)
			}
			if err := UninstallHooks(agent, nil); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			if !strings.Contains(string(b), "echo user") || !strings.Contains(string(b), "keep") {
				t.Fatalf("lost user settings: %s", b)
			}
		})
	}
}

func TestHookInstallRejectsMalformedHooks(t *testing.T) {
	for _, body := range []string{`{"hooks":null}`, `{"hooks":[]}`, `{"hooks":{"stop":42}}`} {
		dir := t.TempDir()
		t.Setenv("SAMCHEONPO_HOME", dir)
		path := filepath.Join(dir, "hooks.json")
		_ = os.WriteFile(path, []byte(body), 0600)
		if err := InstallCursor("/bin/s", path, nil); err == nil {
			t.Fatal("malformed hooks accepted")
		}
		if err := InstallHooks(Codex, "/bin/s", path, nil); err == nil {
			t.Fatal("malformed hooks accepted")
		}
		if b, _ := os.ReadFile(path); string(b) != body {
			t.Fatal("settings mutated")
		}
	}
}
