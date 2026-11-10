//go:build !linux

package live

import "net"

// hookPeerPID is not implemented outside Linux, where process ownership is not
// scanned either.
func hookPeerPID(net.Conn) int { return 0 }
