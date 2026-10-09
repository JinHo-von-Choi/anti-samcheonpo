//go:build windows

package procgroup

import (
	"errors"
	"syscall"
	"time"
)

// RetryTransient runs op again for about a second while it fails because
// another process holds the file (access denied, sharing or lock violation).
func RetryTransient(op func() error) error {
	var err error
	for i := 0; i < 100; i++ {
		if err = op(); err == nil || !transient(err) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return err
}

func transient(err error) bool {
	for _, code := range []syscall.Errno{5, 32, 33} { // ACCESS_DENIED, SHARING_VIOLATION, LOCK_VIOLATION
		if errors.Is(err, code) {
			return true
		}
	}
	return false
}
