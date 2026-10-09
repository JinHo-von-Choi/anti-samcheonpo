//go:build windows

package procgroup

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	createSuspended      = 0x00000004
	createBreakawayJob   = 0x01000000
	detachedProcess      = 0x00000008
	errorAccessDenied    = syscall.Errno(5)
	processResumeAccess  = windows.PROCESS_SET_QUOTA | windows.PROCESS_TERMINATE | windows.PROCESS_SUSPEND_RESUME
	jobKillOnClose       = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	lockfileExclusiveLck = 0x00000002
	lockfileFailNow      = 0x00000001
)

var (
	ntdll          = windows.NewLazySystemDLL("ntdll.dll")
	procNtResume   = ntdll.NewProc("NtResumeProcess")
	kernel32       = windows.NewLazySystemDLL("kernel32.dll")
	procLockFileEx = kernel32.NewProc("LockFileEx")
	jobs           sync.Map // *exec.Cmd -> windows.Handle
)

// attr returns the command's SysProcAttr, creating it when Shell did not.
func attr(cmd *exec.Cmd) *syscall.SysProcAttr {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	return cmd.SysProcAttr
}

// Set marks the command to run in its own process group. Start puts it in a
// Job Object.
func Set(cmd *exec.Cmd) { attr(cmd).CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP }

// Start creates the process suspended, places it in a Job Object that is
// closed with the last handle, and only then lets it run, so no descendant can
// start outside the job.
func Start(cmd *exec.Cmd) error {
	a := attr(cmd)
	a.CreationFlags |= createSuspended
	if err := cmd.Start(); err != nil {
		return err
	}
	job, err := attach(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	jobs.Store(cmd, job)
	return nil
}

func attach(pid int) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = jobKillOnClose
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	proc, err := windows.OpenProcess(processResumeAccess, false, uint32(pid))
	if err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	defer windows.CloseHandle(proc)
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	if r, _, callErr := procNtResume.Call(uintptr(proc)); r != 0 {
		windows.CloseHandle(job)
		return 0, errors.Join(errors.New("NtResumeProcess"), callErr)
	}
	return job, nil
}

// Release closes the command's Job Object, which also ends any process it
// still holds. It is safe to call after Wait and more than once.
func Release(cmd *exec.Cmd) {
	if h, ok := jobs.LoadAndDelete(cmd); ok {
		windows.CloseHandle(h.(windows.Handle))
	}
}

// Kill ends the command and every process in its Job Object. A command that
// was not started through Start falls back to ending its process tree.
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if h, ok := jobs.Load(cmd); ok {
		return windows.TerminateJobObject(h.(windows.Handle), 1)
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	return cmd.Process.Kill()
}

// StartDetached starts the command without a console, in its own group and
// outside the caller's Job Object, so it outlives the caller even when the
// caller's job is closed. A host that forbids leaving its job gets the daemon
// inside it instead. Only cmd.Process is meaningful afterwards.
func StartDetached(cmd *exec.Cmd) error {
	a := attr(cmd)
	a.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess
	a.HideWindow = true
	a.CreationFlags |= createBreakawayJob
	err := cmd.Start()
	if !errors.Is(err, errorAccessDenied) {
		return err
	}
	// a Cmd whose Start failed cannot be started again
	retry := exec.Command(cmd.Path, cmd.Args[1:]...)
	retry.Dir, retry.Env = cmd.Dir, cmd.Env
	retry.Stdin, retry.Stdout, retry.Stderr = cmd.Stdin, cmd.Stdout, cmd.Stderr
	flags := *a
	flags.CreationFlags &^= createBreakawayJob
	retry.SysProcAttr = &flags
	if err := retry.Start(); err != nil {
		return err
	}
	cmd.Process = retry.Process
	return nil
}

// LockExclusive takes a non-blocking exclusive lock on the first byte of the
// file. Windows releases it when the handle is closed or the process exits.
func LockExclusive(f *os.File) error {
	var ol syscall.Overlapped
	r1, _, err := procLockFileEx.Call(f.Fd(), uintptr(lockfileExclusiveLck|lockfileFailNow), 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	if r1 == 0 {
		return err
	}
	return nil
}

// HardLinked reports whether the file has more than one name. FileInfo does
// not carry the link count on Windows, so the file is opened for attributes.
func HardLinked(path string, _ fs.FileInfo) bool {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return false
	}
	return info.NumberOfLinks > 1
}
