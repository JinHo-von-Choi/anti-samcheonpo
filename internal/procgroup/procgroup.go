// Package procgroup holds the few process and file operations that differ
// between Unix and Windows: running a shell command in its own group, killing
// that group, detaching a daemon, taking an exclusive file lock and telling a
// hard-linked file apart.
package procgroup

import "os"

// TempDir is the directory for short per-user paths such as a fallback socket.
func TempDir() string { return os.TempDir() }
