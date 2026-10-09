//go:build windows

package live

import (
	"errors"
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

// sioAFUnixGetPeerPID is the AF_UNIX socket control code that returns the
// process id of the peer.
const sioAFUnixGetPeerPID = 0x58000100

var ownSID = func() *windows.SID {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil
	}
	return user.User.Sid
}()

// samePeerUser reports whether the process on the other end of a unix
// connection runs as this daemon's user. A peer whose identity cannot be read
// is refused; a system that cannot report the peer at all leaves only the
// private directory around the socket as protection.
func samePeerUser(c net.Conn) bool {
	uc, ok := c.(*net.UnixConn)
	if !ok || ownSID == nil {
		return false
	}
	pid, err := peerPID(uc)
	if err != nil {
		return errors.Is(err, windows.WSAEOPNOTSUPP) || errors.Is(err, windows.WSAEINVAL) || errors.Is(err, windows.ERROR_NOT_SUPPORTED)
	}
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(proc)
	var token windows.Token
	if err := windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return false
	}
	return windows.EqualSid(ownSID, user.User.Sid)
}

func peerPID(uc *net.UnixConn) (uint32, error) {
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid uint32
	var ioErr error
	if err := raw.Control(func(fd uintptr) {
		var n uint32
		ioErr = windows.WSAIoctl(windows.Handle(fd), sioAFUnixGetPeerPID, nil, 0, (*byte)(unsafe.Pointer(&pid)), uint32(unsafe.Sizeof(pid)), &n, nil, 0)
	}); err != nil {
		return 0, err
	}
	return pid, ioErr
}
