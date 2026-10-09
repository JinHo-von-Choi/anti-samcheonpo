// Package fakeexe installs stand-in executables for tests. An installed fake is
// a hard link to the running test binary plus a sidecar file naming its
// behavior; when the link is executed, the init function of this package
// performs that behavior and exits before any test code runs. The result works
// on every OS without a shell, a compiler or an interpreter on the host.
package fakeexe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Spec describes what the fake does when it runs.
type Spec struct {
	// Stdout is written verbatim.
	Stdout string
	// Exit is the process exit status.
	Exit int
	// SleepMS delays the exit; the stdout is written first.
	SleepMS int
	// Behavior names a registered program that replaces Stdout/Exit/SleepMS.
	Behavior string
	// Option selects a variant of the behavior.
	Option string
}

const (
	sidecarSuffix = ".fake"
	specEnv       = "FAKEEXE_SPEC"
)

var behaviors = map[string]func(spec Spec, args []string) int{
	"ironlaws":   ironLaws,
	"benchagent": benchAgent,
	"hookstub":   hookStub,
	"argsecho":   argsEcho,
	"spawner":    spawner,
	"dial":       dial,
}

func init() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	// a spawned child reads its behavior from the environment because it is
	// started from the same file as its parent
	raw := []byte(os.Getenv(specEnv))
	if len(raw) == 0 {
		raw, err = os.ReadFile(exe + sidecarSuffix)
		if err != nil {
			return
		}
	}
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		fmt.Fprintln(os.Stderr, "fakeexe: unreadable spec:", err)
		os.Exit(125)
	}
	if spec.Behavior != "" {
		fn, ok := behaviors[spec.Behavior]
		if !ok {
			fmt.Fprintln(os.Stderr, "fakeexe: unknown behavior", spec.Behavior)
			os.Exit(125)
		}
		os.Exit(fn(spec, os.Args[1:]))
	}
	fmt.Fprint(os.Stdout, spec.Stdout)
	if spec.SleepMS > 0 {
		time.Sleep(time.Duration(spec.SleepMS) * time.Millisecond)
	}
	os.Exit(spec.Exit)
}

// Install creates an executable called name in dir and returns its path. On
// Windows the file gets an .exe suffix so that PATH lookups find it.
func Install(t testing.TB, dir, name string, spec Spec) string {
	t.Helper()
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		name += ".exe"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	base, err := baseCopy()
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, name)
	_ = os.Remove(dst)
	if err := os.Link(base, dst); err != nil {
		if err := copyFile(base, dst); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst+sidecarSuffix, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

var (
	baseOnce sync.Once
	basePath string
	baseErr  error
)

// baseCopy keeps one private copy of the test binary per process under the
// temp directory so every fake is a cheap hard link to it. Copies left by
// earlier runs are removed once they are an hour old.
func baseCopy() (string, error) {
	baseOnce.Do(func() {
		self, err := os.Executable()
		if err != nil {
			baseErr = err
			return
		}
		st, err := os.Stat(self)
		if err != nil {
			baseErr = err
			return
		}
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", self, st.Size(), st.ModTime().UnixNano())))
		dir := filepath.Join(os.TempDir(), "samcheonpo-fakeexe")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			baseErr = err
			return
		}
		pruneOld(dir, time.Hour)
		name := hex.EncodeToString(sum[:8])
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		basePath = filepath.Join(dir, name)
		if _, err := os.Stat(basePath); err == nil {
			return
		}
		tmp := basePath + ".tmp"
		if err := copyFile(self, tmp); err != nil {
			baseErr = err
			return
		}
		if err := os.Rename(tmp, basePath); err != nil {
			_ = os.Remove(tmp)
			if _, statErr := os.Stat(basePath); statErr != nil {
				baseErr = err
			}
		}
	})
	return basePath, baseErr
}

func pruneOld(dir string, age time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && time.Since(info.ModTime()) > age {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
