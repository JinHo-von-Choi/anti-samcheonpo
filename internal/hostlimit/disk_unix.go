//go:build !windows

package hostlimit

import (
	"io/fs"
	"syscall"

	"golang.org/x/sys/unix"
)

// SpaceOf reports the file system space of path.
func SpaceOf(path string) Disk {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Disk{}
	}
	bs := uint64(st.Bsize)
	return Disk{Free: uint64(st.Bavail) * bs, Total: uint64(st.Blocks) * bs, Known: true}
}

// blocks is the space a file takes on disk, holes excluded.
func blocks(info fs.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return info.Size()
}
