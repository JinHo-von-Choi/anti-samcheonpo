package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHermesPluginInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(home, ".samcheonpo"))
	t.Setenv("HERMES_HOME", filepath.Join(home, ".hermes"))
	var calls []string
	fail := false
	hermesCLI = func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if fail {
			return []byte("no"), errors.New("exit 1")
		}
		if args[1] == "remove" {
			_ = os.RemoveAll(HermesPluginDir())
		}
		return nil, nil
	}
	t.Cleanup(func() { hermesCLI = nil })
	dir := HermesPluginDir()
	if err := InstallHermes("/opt/samcheonpo", dir, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "__init__.py"))
	if !strings.Contains(string(b), `os.environ.get("SAMCHEONPO_BIN") or "/opt/samcheonpo"`) {
		t.Fatalf("the plugin calls the installed hook client: %s", b[:400])
	}
	if _, err := os.Stat(filepath.Join(dir, "plugin.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := InstallHermes("/opt/samcheonpo", dir, nil); err == nil {
		t.Fatal("a second install is refused")
	}
	if err := UninstallHermes(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("uninstall removes the plugin")
	}
	if strings.Join(calls, ";") != "plugins enable samcheonpo;plugins remove samcheonpo" {
		t.Fatalf("hermes manages its own config: %v", calls)
	}
	// a failed enable leaves nothing behind
	fail = true
	if err := InstallHermes("/opt/samcheonpo", dir, nil); err == nil {
		t.Fatal("enable failure is reported")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("files are removed after a failed enable")
	}
}

// A bytecode cache left from an earlier version belongs to the old source.
// The installer rewrites the source, so it removes the stale cache instead of
// leaving a cache that no longer matches what is installed.
func TestHermesInstallRemovesStaleBytecodeCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(home, ".samcheonpo"))
	t.Setenv("HERMES_HOME", filepath.Join(home, ".hermes"))
	hermesCLI = func(args ...string) ([]byte, error) {
		if args[1] == "remove" {
			_ = os.RemoveAll(HermesPluginDir())
		}
		return nil, nil
	}
	t.Cleanup(func() { hermesCLI = nil })
	dir := HermesPluginDir()
	if err := os.MkdirAll(filepath.Join(dir, "__pycache__"), 0o755); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "__pycache__", "__init__.cpython-314.pyc")
	if err := os.WriteFile(cache, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallHermes("/opt/samcheonpo", dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("the stale cache is removed")
	}
}
