package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/procgroup"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testutil/fakeexe"
)

// The agent hands every installed command to its shell as one string. These
// tests run the strings that installation wrote, through the shell the harness
// uses, against a stand-in binary installed in a directory whose name has a
// space in it.
func installedBinary(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Program Files", "samcheonpo")
	return fakeexe.Install(t, dir, "samcheonpo", fakeexe.Spec{Behavior: "argsecho"})
}

func runInShell(t *testing.T, command string) string {
	t.Helper()
	out, err := procgroup.Shell(command).Output()
	if err != nil {
		t.Fatalf("%s: %q: %v", procgroup.ShellName(), command, err)
	}
	return strings.TrimSpace(string(out))
}

func TestInstalledPluginHookCommandsRunInTheAgentShell(t *testing.T) {
	bin := installedBinary(t)
	plugin := filepath.Join(t.TempDir(), "plugin")
	if err := writePlugin(plugin, bin); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(plugin, "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string } `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for event, groups := range doc.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if got := runInShell(t, h.Command); got != "hook|"+event {
					t.Errorf("%s: the hook command ran with %q", event, got)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Fatal("no hook commands were checked")
	}
}

func TestInstalledSlashCommandsRunInTheAgentShell(t *testing.T) {
	bin := installedBinary(t)
	plugin := filepath.Join(t.TempDir(), "plugin")
	if err := writePlugin(plugin, bin); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(plugin, "commands", "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no slash commands: %v", err)
	}
	inline := regexp.MustCompile("!`([^`]+)`")
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		m := inline.FindSubmatch(raw)
		if m == nil {
			t.Fatalf("%s has no command line", filepath.Base(f))
		}
		command := strings.ReplaceAll(string(m[1]), "$ARGUMENTS", "normal")
		got := runInShell(t, command)
		if !strings.HasPrefix(got, "cmd|") {
			t.Errorf("%s: the command ran with %q", filepath.Base(f), got)
		}
		if allow := regexp.MustCompile(`allowed-tools: Bash\((.+) cmd:\*\)`).FindSubmatch(raw); allow == nil || string(allow[1]) != bin {
			t.Errorf("%s: the permission rule must name the binary the command runs", filepath.Base(f))
		}
	}
}

func TestInstalledStatuslineCommandRunsInTheAgentShell(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(home, ".samcheonpo"))
	settings := filepath.Join(home, "settings.json")
	bin := installedBinary(t)
	if err := Install(Options{Binary: bin, SettingsPath: settings}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		StatusLine struct{ Command string } `json:"statusLine"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if got := runInShell(t, s.StatusLine.Command); got != "statusline" {
		t.Fatalf("the status line command ran with %q", got)
	}
}

func TestInstalledHookCommandUsesTheSiblingTransport(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Program Files", "samcheonpo")
	main := fakeexe.Install(t, dir, "samcheonpo", fakeexe.Spec{Behavior: "argsecho"})
	fakeexe.Install(t, dir, "samcheonpo-hook", fakeexe.Spec{Behavior: "argsecho"})
	plugin := filepath.Join(t.TempDir(), "plugin")
	if err := writePlugin(plugin, main); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(plugin, "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "samcheonpo-hook"+hookclient.ExeSuffix) {
		t.Fatalf("the hook did not select the sibling transport: %s", raw)
	}
}
