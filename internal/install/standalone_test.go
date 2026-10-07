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
