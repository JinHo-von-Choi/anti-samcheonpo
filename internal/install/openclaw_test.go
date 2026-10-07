package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenclawPluginInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(home, ".samcheonpo"))
	var calls []string
	failAt := ""
	openclawCLI = func(args ...string) ([]byte, error) {
		c := strings.Join(args, " ")
		calls = append(calls, c)
		if failAt != "" && strings.HasPrefix(c, failAt) {
			return []byte("no"), errors.New("exit 1")
		}
		return nil, nil
	}
	t.Cleanup(func() { openclawCLI = nil })
	dir := OpenclawSourceDir()
	if err := InstallOpenclaw("/opt/samcheonpo", nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "index.js"))
	if !strings.Contains(string(b), `process.env.SAMCHEONPO_BIN ?? "/opt/samcheonpo"`) {
		t.Fatal("the plugin calls the installed hook client")
	}
	for _, f := range []string{"package.json", "openclaw.plugin.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	if err := UninstallOpenclaw(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("uninstall removes the source files")
	}
	want := "plugins install " + dir + ";config set plugins.entries.samcheonpo.hooks.allowConversationAccess true;plugins uninstall samcheonpo --force"
	if strings.Join(calls, ";") != want {
		t.Fatalf("openclaw manages its own config:\n%v", calls)
	}
	// a failed config step undoes the install
	calls, failAt = nil, "config set"
	if err := InstallOpenclaw("/opt/samcheonpo", nil); err == nil {
		t.Fatal("config failure is reported")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) || calls[len(calls)-1] != "plugins uninstall samcheonpo --force" {
		t.Fatalf("a failed install leaves nothing behind: %v", calls)
	}
}
