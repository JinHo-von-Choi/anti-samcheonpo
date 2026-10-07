//go:build linux

package bench

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
)

// stopVerifiedDaemon signals the pid only after /proc shows it is a samcheonpo
// daemon of this home, then waits for it to leave.
func stopVerifiedDaemon(pid int, home string) bool {
	env, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if os.IsNotExist(err) {
		return true
	}
	if err != nil || !strings.Contains(string(env), "SAMCHEONPO_HOME="+home+"\x00") {
		return false
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return errors.Is(err, syscall.ESRCH)
	}
	for i := 0; i < 100; i++ {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
