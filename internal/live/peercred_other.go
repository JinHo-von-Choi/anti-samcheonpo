//go:build !linux

package live

import "net"

// samePeerUser relies on the socket's 0600 permission where peer credentials
// are not read.
func samePeerUser(net.Conn) bool { return true }
