// Package sockpath resolves the daemon socket path. It is shared by the hook
// client and the daemon and imports nothing heavy.
package sockpath

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
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
	if len(p) <= maxLen {
		return p
	}
	h := sha256.Sum256([]byte(p))
	return filepath.Join("/tmp", fmt.Sprintf("samcheonpo-%d-%s.sock", os.Getuid(), hex.EncodeToString(h[:6])))
}
