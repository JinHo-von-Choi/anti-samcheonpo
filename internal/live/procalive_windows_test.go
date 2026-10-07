//go:build windows

package live

import "os"

// processAlive on Windows only asks whether the pid can be opened; the daemon
// flow tests that use it do not run here.
func processAlive(pid int) bool {
	_, err := os.FindProcess(pid)
	return err == nil
}
