//go:build linux

package hostlimit

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	limitPath = "/proc/sys/fs/inotify/max_user_instances"
	procPath  = "/proc"
)

// Inotify counts the watch instances the current user holds and reads the
// per-user limit. Both numbers come from the running kernel: the limit from
// sysctl, the count from the file descriptors that point at inotify.
//
// The count covers every process owned by this user, because the kernel
// charges the limit per user, not per process. A failure to read either side
// yields an unknown budget rather than a zero one.
func Inotify() Watch {
	w := Watch{}
	limit, ok := InotifyLimit()
	if !ok {
		return w
	}
	w.Limit = limit
	w.Known = true
	w.Used = countInstances()
	return w
}

// countInstances returns how many inotify instances this user holds. An
// unreadable /proc entry is skipped rather than counted, so a process that
// exits mid-scan does not make the result negative.
func countInstances() int {
	uid := os.Getuid()
	entries, err := os.ReadDir(procPath)
	if err != nil {
		return 0
	}
	used := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		dir := filepath.Join(procPath, e.Name())
		if !ownedByUID(dir, uid) {
			continue
		}
		used += countProcessInstances(pid)
	}
	return used
}

// ownedByUID reports whether the process directory belongs to the given user.
// A process that vanished or is unreadable is treated as not ours.
func ownedByUID(dir string, uid int) bool {
	info, err := os.Stat(dir)
	if err != nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == uid
}

// countProcessInstances counts one process's inotify descriptors. Each
// fsnotify watcher holds exactly one, so the descriptor count is the
// instance count.
func countProcessInstances(pid int) int {
	fdDir := filepath.Join(procPath, strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		return 0
	}
	used := 0
	for _, fd := range entries {
		target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
		if err != nil {
			continue
		}
		if strings.Contains(target, "anon_inode:inotify") {
			used++
		}
	}
	return used
}

// InotifyLimit returns the per-user watch instance limit without counting
// the instances in use. Reading the limit is one small file read; counting
// walks every process descriptor, which is far too slow for a hook path.
// A caller that needs the count must say so by asking for Inotify.
func InotifyLimit() (int, bool) {
	raw, err := os.ReadFile(limitPath)
	if err != nil {
		return 0, false
	}
	limit, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || limit <= 0 {
		return 0, false
	}
	return limit, true
}
