package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mcodeHooksFile(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(home, ".samcheonpo"))
	p := filepath.Join(home, ".minimax", "hooks.json")
	if body != "" {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// TestMcodeHooksTurnTheFileOnAndOff covers a fresh file: mcode runs nothing
// unless the top level says enabled, and the limit field is timeoutSeconds.
func TestMcodeHooksTurnTheFileOnAndOff(t *testing.T) {
	p := mcodeHooksFile(t, "")
	if err := InstallHooks(Mcode, "/opt/samcheonpo", p, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	var doc struct {
		Enabled bool `json:"enabled"`
		Hooks   map[string][]struct {
			Hooks []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Enabled || len(doc.Hooks) != len(Mcode.Events) {
		t.Fatalf("enabled=%v events=%d: %s", doc.Enabled, len(doc.Hooks), b)
	}
	h := doc.Hooks["Stop"][0].Hooks[0]
	if h["timeoutSeconds"] != float64(160) || h["timeout"] != nil || !strings.HasSuffix(h["command"].(string), " hook Stop mcode") {
		t.Fatalf("handler %v", h)
	}
	st, err := InspectHooks(Mcode)
	if err != nil || !st.Installed || st.Off || len(st.Missing) != 0 {
		t.Fatalf("inspect %+v %v", st, err)
	}
	if err := UninstallHooks("mcode", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("a file the install created is removed: %v", err)
	}
}

// TestMcodeHooksNeverStartOtherHooks refuses to turn on a file whose hooks
// the user switched off or never turned on.
func TestMcodeHooksNeverStartOtherHooks(t *testing.T) {
	for name, body := range map[string]string{
		"off":     `{"enabled":false,"hooks":{}}`,
		"dormant": `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"mine.sh"}]}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := mcodeHooksFile(t, body)
			if err := InstallHooks(Mcode, "/opt/samcheonpo", p, nil); err == nil {
				t.Fatal("install must refuse")
			}
			b, _ := os.ReadFile(p)
			if string(b) != body {
				t.Fatalf("file changed: %s", b)
			}
			if _, err := os.Stat(hookStatePath("mcode")); !os.IsNotExist(err) {
				t.Fatal("a refused install leaves no record")
			}
		})
	}
}

// TestMcodeHooksKeepAnEnabledFile merges into a file that is already on and
// leaves it on with the user's hooks after uninstall.
func TestMcodeHooksKeepAnEnabledFile(t *testing.T) {
	orig := `{"enabled":true,"hooks":{"Stop":[{"hooks":[{"type":"command","command":"mine.sh","timeoutSeconds":3}]}]}}`
	p := mcodeHooksFile(t, orig)
	if err := InstallHooks(Mcode, "/opt/samcheonpo", p, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "mine.sh") {
		t.Fatalf("user hook lost: %s", b)
	}
	if err := UninstallHooks("mcode", nil); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	var after, before any
	_ = json.Unmarshal(b, &after)
	_ = json.Unmarshal([]byte(orig), &before)
	ja, _ := json.Marshal(after)
	jb, _ := json.Marshal(before)
	if string(ja) != string(jb) {
		t.Fatalf("uninstall restores the file:\n%s\n%s", ja, jb)
	}
}

func TestMcodeInspectReportsAFileTurnedOff(t *testing.T) {
	p := mcodeHooksFile(t, "")
	if err := InstallHooks(Mcode, "/opt/samcheonpo", p, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	var root map[string]json.RawMessage
	_ = json.Unmarshal(b, &root)
	root["enabled"] = json.RawMessage("false")
	out, _ := json.Marshal(root)
	_ = os.WriteFile(p, out, 0o600)
	st, err := InspectHooks(Mcode)
	if err != nil || !st.Off {
		t.Fatalf("a file turned off must be reported: %+v %v", st, err)
	}
}
