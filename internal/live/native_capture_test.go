package live

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentNativeHostCaptures(t *testing.T) {
	for _, host := range []struct {
		agent, version string
		minimum        int
	}{
		{"codex", "0.166.0", 12}, {"claude", "2.1.317", 12}, {"agy", "1.4.12", 12},
	} {
		t.Run(host.agent, func(t *testing.T) {
			root := filepath.Join("../../testdata/hooks", host.agent, host.version)
			rawManifest, err := os.ReadFile(filepath.Join(root, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			var manifest struct {
				Captures []struct{ File, SHA256 string }
			}
			if err := json.Unmarshal(rawManifest, &manifest); err != nil {
				t.Fatal(err)
			}
			digests := map[string]string{}
			for _, c := range manifest.Captures {
				digests[c.File] = c.SHA256
			}
			files, err := filepath.Glob(filepath.Join("../../testdata/hooks", host.agent, host.version, "*.json"))
			if err != nil || len(files) < host.minimum+1 {
				t.Fatalf("missing captures: %v", err)
			}
			for _, p := range files {
				if filepath.Base(p) == "manifest.json" {
					continue
				}
				raw, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != digests[filepath.Base(p)] {
					t.Fatalf("capture digest mismatch: %s", p)
				}
				name, _, _ := strings.Cut(filepath.Base(p), "-")
				names, ins, err := translateIn(host.agent, name, json.RawMessage(raw))
				if err != nil || len(names) == 0 && !(host.agent == "agy" && (name == "PostInvocation" || name == "PostToolUse")) {
					t.Fatalf("%s: %v", p, err)
				}
				for i, n := range names {
					if reason := hookShape(n, ins[i]); reason != "" {
						t.Fatalf("%s: %s", p, reason)
					}
				}
			}
		})
	}
}
