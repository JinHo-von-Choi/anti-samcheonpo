//go:build windows

package procgroup

import (
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

// Shell runs command through the command interpreter (%COMSPEC%, cmd.exe).
func Shell(command string) *exec.Cmd {
	sh := os.Getenv("COMSPEC")
	if sh == "" {
		sh = "cmd.exe"
	}
	return exec.Command(sh, "/C", command)
}

// Set puts the command in its own process group.
func Set(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// Kill ends the command and every process it started.
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	return cmd.Process.Kill()
}

// Detach starts the command without a console, in its own group, so it
// outlives the caller.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008 /* DETACHED_PROCESS */, HideWindow: true}
}

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx = kernel32.NewProc("LockFileEx")
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
)

// LockExclusive takes a non-blocking exclusive lock on the first byte of the
// file. Windows releases it when the handle is closed or the process exits.
func LockExclusive(f *os.File) error {
	var ol syscall.Overlapped
	r1, _, err := procLockFileEx.Call(f.Fd(), uintptr(lockfileExclusiveLock|lockfileFailImmediately), 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	if r1 == 0 {
		return err
	}
	return nil
}

// HardLinked cannot be read from FileInfo on Windows; a file is treated as
// having one name.
func HardLinked(fs.FileInfo) bool { return false }
