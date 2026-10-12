//go:build windows

package eval

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockLabelFile(f *os.File) error {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrLabelsBusy
	}
	return err
}
