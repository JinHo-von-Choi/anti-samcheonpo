//go:build !windows

package procgroup

import (
	"io/fs"
	"os"
	"os/exec"
	"syscall"
)

// Shell runs command through /bin/sh.
func Shell(command string) *exec.Cmd { return exec.Command("/bin/sh", "-c", command) }

// Set puts the command in its own process group so Kill can end it whole.
func Set(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// Kill ends the command's whole process group.
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// Detach starts the command in its own session so it outlives the caller.
func Detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// LockExclusive takes a non-blocking exclusive lock that the OS releases when
// the process exits.
func LockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// HardLinked reports whether the file has more than one name.
func HardLinked(fi fs.FileInfo) bool {
	sys, ok := fi.Sys().(*syscall.Stat_t)
	return ok && sys.Nlink > 1
}
