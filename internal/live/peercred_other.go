//go:build !linux && !darwin && !windows

package live

import "net"

// samePeerUser relies on the socket living in a directory only this user can
// reach where peer credentials are not read.
func samePeerUser(net.Conn) bool { return true }
