//go:build !windows

package live

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// processAlive treats a zombie (killed, not yet reaped because the init
// process of a container does not reap orphans) as dead.
func processAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	// the state follows the parenthesized command name
	if i := strings.LastIndexByte(string(b), ')'); i >= 0 && i+2 < len(b) {
		return b[i+2] != 'Z'
	}
	return true
}
