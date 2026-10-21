package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStandaloneConnectionsPreserveModifications(t *testing.T) {
	for _, agent := range []string{"copilot", "opencode"} {
		t.Run(agent, func(t *testing.T) {
			t.Setenv("SAMCHEONPO_HOME", t.TempDir())
			path := filepath.Join(t.TempDir(), "connection")
			if err := installStandalone(agent, path, []byte("original")); err != nil {
				t.Fatal(err)
			}
			if err := installStandalone(agent, path, []byte("overwrite")); err == nil {
				t.Fatal("existing connection overwritten")
			}
			_ = os.WriteFile(path, []byte("user modification"), 0600)
			if err := removeStandalone(agent, path); err == nil {
				t.Fatal("modified connection removed")
			}
			if b, _ := os.ReadFile(path); string(b) != "user modification" {
				t.Fatal("modification lost")
			}
			_ = os.WriteFile(path, []byte("original"), 0600)
			if err := removeStandalone(agent, path); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("unchanged generated file retained")
			}
		})
	}
}

func TestInstallOpencodeReinstallKeepsOriginal(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	t.Setenv("OPENCODE_CONFIG_DIR", t.TempDir())
	path := OpencodePluginPath()
	if err := InstallOpencode("/usr/local/bin/samcheonpo", path, nil); err != nil {
		t.Fatal(err)
	}
	orig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallOpencode("/elsewhere/bin/samcheonpo", path, nil); err == nil {
		t.Fatal("reinstall over an existing connection was allowed")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(orig) {
		t.Fatal("reinstall modified the existing connection file")
	}
}
