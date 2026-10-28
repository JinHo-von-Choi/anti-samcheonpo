//go:build windows

package hostlimit

import (
	"io/fs"

	"golang.org/x/sys/windows"
)

// SpaceOf reports the file system space of path.
func SpaceOf(path string) Disk {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Disk{}
	}
	var free, total, all uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &all); err != nil {
		return Disk{}
	}
	return Disk{Free: free, Total: total, Known: true}
}

// blocks is the file size: Windows states no block count here.
func blocks(info fs.FileInfo) int64 { return info.Size() }
