package install

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/procgroup"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sockpath"
)

// Opt-in native binary smoke; package-release.sh supplies both built binaries.
// The environment is an allowlist with no API credentials or real user state.
func TestCleanEnvironmentBinarySmoke(t *testing.T) {
	bin := os.Getenv("SAMCHEONPO_SMOKE_BIN")
	if bin == "" {
		t.Skip("set SAMCHEONPO_SMOKE_BIN to a native build")
	}
	var err error
	bin, err = filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAMCHEONPO_HOME", home)
	env := []string{"PATH=" + os.Getenv("PATH"), "SAMCHEONPO_HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(root, "claude"), "SAMCHEONPO_NO_SPAWN=0"}
	run := func(stdin string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = env
		cmd.Dir = project
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return out
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "hook", "Shutdown")
		cmd.Env = append(env, "SAMCHEONPO_NO_SPAWN=1")
		cmd.Dir = project
		cmd.Stdin = strings.NewReader("{}")
		_ = cmd.Run()
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(sockpath.Path()); os.IsNotExist(err) {
				// the socket goes before the final ledger and receipt writes;
				// the folder may only be removed once the daemon has exited,
				// which is when its lock can be taken
				daemonExited(t, filepath.Join(home, "run", "daemon.lock"))
				return
			}
		}
		t.Error("isolated smoke daemon did not stop")
	})
	if out := run("", "doctor", "--format", "json"); !json.Valid(out) {
		t.Fatalf("doctor emitted invalid JSON: %s", out)
	}
	settings := filepath.Join(root, "settings.json")
	before := []byte(`{"model":"user-model","statusLine":{"type":"command","command":"echo user"},"custom":{"keep":true}}`)
	if err := os.WriteFile(settings, before, 0600); err != nil {
		t.Fatal(err)
	}
	run("", "install", "--agent", "claude", "--settings", settings, "--no-plugin-cli")
	run("", "uninstall", "--agent", "claude")
	after, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var x, y any
	if json.Unmarshal(before, &x) != nil || json.Unmarshal(after, &y) != nil || !reflect.DeepEqual(x, y) {
		t.Fatal("install/uninstall changed unrelated user settings")
	}
	hooks := filepath.Join(root, "hooks.json")
	if err := os.WriteFile(hooks, []byte(`{"custom":"preserved","hooks":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	run("", "install", "--agent", "codex", "--hooks-file", hooks)
	run("", "uninstall", "--agent", "codex")
	after, err = os.ReadFile(hooks)
	if err != nil || !bytes.Contains(after, []byte("preserved")) || bytes.Contains(after, []byte("samcheonpo-hook")) {
		t.Fatal("codex removal failed", err)
	}
	transcripts := filepath.Join(root, "transcripts")
	if err := os.MkdirAll(filepath.Join(transcripts, "project"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../../testdata/claude/basic.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(transcripts, "project", "basic.jsonl"), fixture, 0600); err != nil {
		t.Fatal(err)
	}
	out := run("", "audit", "--since", "all", "--agent", "claude", "--claude-dir", transcripts, "--codex-dir", filepath.Join(root, "empty"), "--format", "json")
	if !bytes.Contains(out, []byte("s1")) {
		t.Fatalf("offline audit did not read fixture: %s", out)
	}
	if out = run("", "receipt", "s1", "--format", "json"); !json.Valid(out) {
		t.Fatalf("offline receipt failed: %s", out)
	}
	payload, _ := json.Marshal(map[string]string{"session_id": "offline-smoke", "cwd": project, "prompt": "API 키 없이 원래 목표를 기록한다"})
	run(string(payload), "hook", "UserPromptSubmit")
	// Command without a session ID chooses the only live session in this project.
	out = run("", "cmd", "card")
	if !bytes.Contains(out, []byte("API 키 없이 원래 목표")) || !bytes.Contains(out, []byte("미승인")) {
		t.Fatalf("basic monitor lost draft: %s", out)
	}
}

// daemonExited waits until nothing holds the daemon's lock file any more.
func daemonExited(t *testing.T, lock string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		f, err := os.OpenFile(lock, os.O_RDWR, 0)
		if err != nil {
			return
		}
		held := procgroup.LockExclusive(f) != nil
		f.Close()
		if !held {
			return
		}
	}
	t.Error("the isolated smoke daemon kept running after its socket was removed")
}
