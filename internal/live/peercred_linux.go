//go:build linux

package live

import (
	"net"
	"os"
	"syscall"
)

// samePeerUser reports whether the process on the other end of a unix
// connection runs as this daemon's user.
func samePeerUser(c net.Conn) bool {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return false
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil || credErr != nil || cred == nil {
		return false
	}
	return int(cred.Uid) == os.Getuid()
}
