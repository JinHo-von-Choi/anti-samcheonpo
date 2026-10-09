// Package procgroup holds the few process and file operations that differ
// between Unix and Windows: running a shell command in its own group, killing
// that group, detaching a daemon, taking an exclusive file lock and telling a
// hard-linked file apart.
//
// A command built with Shell and Set is started with Start, ended with Kill
// and released with Release once Wait has returned. On Unix the group is a
// process group; on Windows it is a Job Object that also ends every
// descendant, including processes that outlived their parent.
package procgroup

import "os"

// TempDir is the directory for short per-user paths such as a fallback socket.
func TempDir() string { return os.TempDir() }

// MkdirAllPrivate creates path and any missing parents so that only the
// current user can reach what is stored below it. A directory that already
// exists keeps the permissions its owner gave it.
func MkdirAllPrivate(path string) error {
	_, statErr := os.Stat(path)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if statErr == nil {
		return nil
	}
	return restrictToUser(path)
}

// Rename renames oldpath over newpath. Windows refuses the rename while
// another process still has newpath open (an editor, a virus scanner, a
// concurrent reader), so it retries briefly before giving up.
func Rename(oldpath, newpath string) error {
	return RetryTransient(func() error { return os.Rename(oldpath, newpath) })
}
