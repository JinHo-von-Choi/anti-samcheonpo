//go:build linux

package procscan

import (
	"io/fs"
	"syscall"
)

func ownerOf(info fs.FileInfo) int {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}
