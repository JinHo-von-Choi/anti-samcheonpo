//go:build linux

package live

import (
	"net"
	"syscall"
)

// hookPeerPID returns the process on the other end of a unix socket (0: unknown).
func hookPeerPID(c net.Conn) int {
	u, ok := c.(*net.UnixConn)
	if !ok {
		return 0
	}
	raw, err := u.SyscallConn()
	if err != nil {
		return 0
	}
	pid := 0
	_ = raw.Control(func(fd uintptr) {
		if cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED); err == nil {
			pid = int(cred.Pid)
		}
	})
	return pid
}
