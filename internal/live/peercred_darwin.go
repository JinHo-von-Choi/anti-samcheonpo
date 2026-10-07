//go:build darwin

package live

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// samePeerUser reports whether the process on the other end of a unix
// connection runs as this daemon's user (LOCAL_PEERCRED).
func samePeerUser(c net.Conn) bool {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return false
	}
	var cred *unix.Xucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil || credErr != nil || cred == nil {
		return false
	}
	return int(cred.Uid) == os.Getuid()
}
