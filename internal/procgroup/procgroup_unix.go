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

// ShellName names the interpreter Shell uses.
func ShellName() string { return "sh" }

// Set puts the command in its own process group so Kill can end it whole.
func Set(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// Start starts a command prepared with Set.
func Start(cmd *exec.Cmd) error { return cmd.Start() }

// Release frees what Start attached to the command. It is safe to call after
// Wait and more than once.
func Release(*exec.Cmd) {}

// Kill ends the command's whole process group.
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// StartDetached starts the command in its own session so it outlives the
// caller.
func StartDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// LockExclusive takes a non-blocking exclusive lock that the OS releases when
// the process exits.
func LockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// HardLinked reports whether the file has more than one name.
func HardLinked(_ string, fi fs.FileInfo) bool {
	sys, ok := fi.Sys().(*syscall.Stat_t)
	return ok && sys.Nlink > 1
}
