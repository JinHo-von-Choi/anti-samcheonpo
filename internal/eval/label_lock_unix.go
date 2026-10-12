//go:build !windows

package eval

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockLabelFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return ErrLabelsBusy
	}
	return err
}
