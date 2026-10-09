//go:build windows

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The claude command npm installs is a batch shim. Arguments with spaces must
// reach it intact when installation registers the plugin.
func TestPluginRegistrationReachesBatchShim(t *testing.T) {
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(base, "calls.txt")
	shim := "@echo off\r\n>>\"" + calls + "\" echo [%~1][%~2][%~3][%~4]\r\n"
	if err := os.WriteFile(filepath.Join(bin, "claude.cmd"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	home := filepath.Join(base, "Home With Space", ".samcheonpo")
	t.Setenv("SAMCHEONPO_HOME", home)
	settings := filepath.Join(base, "settings.json")
	if err := Install(Options{Binary: filepath.Join(base, "samcheonpo.exe"), SettingsPath: settings, UsePluginCLI: true}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("the shim was never called: %v", err)
	}
	want := "[plugin][marketplace][add][" + filepath.Join(home, "plugin") + "]"
	if !strings.Contains(string(got), want) {
		t.Fatalf("the marketplace path did not arrive intact:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(string(got), "[plugin][install][samcheonpo@"+MarketplaceName+"][--scope]") {
		t.Fatalf("plugin install call missing: %q", got)
	}
}
