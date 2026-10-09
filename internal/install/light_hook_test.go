package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
)

func TestLightHookInstallPreservesMainCommandsAndRemovalMarker(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	dir := t.TempDir()
	main := filepath.Join(dir, "samcheonpo"+hookclient.ExeSuffix)
	helper := filepath.Join(dir, "samcheonpo-hook"+hookclient.ExeSuffix)
	if err := os.WriteFile(helper, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(dir, "plugin")
	if err := writePlugin(plugin, main); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(plugin, "hooks", "hooks.json"))
	if err != nil || !json.Valid(b) || !strings.Contains(string(b), "samcheonpo-hook") {
		t.Fatalf("hook path not selected: %v", err)
	}
	b, err = os.ReadFile(filepath.Join(plugin, "commands", "accept.md"))
	if err != nil || strings.Contains(string(b), "samcheonpo-hook") || !strings.Contains(string(b), "samcheonpo") {
		t.Fatalf("main command was replaced: %v", err)
	}
	hooks := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(hooks, []byte(`{"custom":"preserve"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallHooks(Codex, main, hooks, nil); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(hooks)
	if err != nil || !strings.Contains(string(b), "samcheonpo-hook") {
		t.Fatalf("codex did not select helper: %v", err)
	}
	// Removal uses recorded identity, even if the helper was subsequently removed.
	if err := os.Remove(helper); err != nil {
		t.Fatal(err)
	}
	if err := UninstallHooks("codex", func(string) {}); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(hooks)
	if err != nil || !strings.Contains(string(b), "preserve") || strings.Contains(string(b), "samcheonpo-hook") {
		t.Fatalf("uninstall damaged unrelated settings: %v", err)
	}
}
