// Package sockpath resolves the daemon socket path. It is shared by the hook
// client and the daemon and imports nothing heavy.
package sockpath

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
)

// maxLen keeps the path under the unix socket limit (108 bytes on Linux,
// 104 on macOS) including the terminator.
const maxLen = 100

// Home returns the samcheonpo home directory.
func Home() string {
	if h := os.Getenv("SAMCHEONPO_HOME"); h != "" {
		return h
	}
	d, _ := os.UserHomeDir()
	return filepath.Join(d, ".samcheonpo")
}

// Path returns the socket path: $XDG_RUNTIME_DIR/samcheonpo.sock for the
// default home, else <home>/run/samcheonpo.sock, so an explicit
// SAMCHEONPO_HOME never shares the default daemon. A path too long for a unix
// socket is replaced by a short per-user path in /tmp derived from it.
func Path() string {
	p := filepath.Join(Home(), "run", "samcheonpo.sock")
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" && os.Getenv("SAMCHEONPO_HOME") == "" {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			p = filepath.Join(d, "samcheonpo.sock")
		}
	}
	return shortPath(p, os.TempDir(), UserID())
}

// Candidates lists the sockets a client may find the daemon on, Path first.
// The daemon listens where its own environment points, and a service started
// without XDG_RUNTIME_DIR resolves a different path from the login session
// that started the daemon. An explicit SAMCHEONPO_HOME has one socket.
func Candidates() []string {
	out := []string{Path()}
	if os.Getenv("SAMCHEONPO_HOME") != "" {
		return out
	}
	paths := []string{filepath.Join(Home(), "run", "samcheonpo.sock")}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		paths = append(paths, filepath.Join(d, "samcheonpo.sock"))
	}
	if runtime.GOOS == "linux" {
		paths = append(paths, filepath.Join("/run/user", UserID(), "samcheonpo.sock"))
	}
	for _, p := range paths {
		if dir, err := os.Stat(filepath.Dir(p)); err != nil || !dir.IsDir() {
			continue
		}
		if s := shortPath(p, os.TempDir(), UserID()); !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// MissLogPath is where a hook that reached no daemon leaves its record.
func MissLogPath() string { return filepath.Join(Home(), "run", "hook-misses.jsonl") }

func UserID() string {
	user := fmt.Sprint(os.Getuid())
	if os.Getuid() < 0 {
		user = os.Getenv("USERNAME")
	}
	return user
}

// ForHome resolves an explicit home without mutating the caller's environment.
func ForHome(home, tmp, user string) string {
	return shortPath(filepath.Join(home, "run", "samcheonpo.sock"), tmp, user)
}

func shortPath(p, tmp, user string) string {
	if len(p) <= maxLen {
		return p
	}
	h := sha256.Sum256([]byte(p))
	fallback := filepath.Join(tmp, fmt.Sprintf("samcheonpo-%s-%s.sock", user, hex.EncodeToString(h[:6])))
	if len(fallback) <= maxLen {
		return fallback
	}
	// a long temp directory or user name: keep only what makes the name unique
	return filepath.Join(tmp, "sc-"+hex.EncodeToString(h[:6])+".sock")
}
