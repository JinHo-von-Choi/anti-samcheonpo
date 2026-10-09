//go:build windows

package live

import "golang.org/x/sys/windows"

// processAlive reports whether the process is still running. A process that
// has ended can stay openable while some other process holds a handle to it,
// so being able to open it proves nothing; its exit code does.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	const stillActive = 259
	return code == stillActive
}
