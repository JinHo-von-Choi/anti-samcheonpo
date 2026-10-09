//go:build windows

package clock

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32      = windows.NewLazySystemDLL("kernel32.dll")
	procFrequency = kernel32.NewProc("QueryPerformanceFrequency")
	procCounter   = kernel32.NewProc("QueryPerformanceCounter")
	frequency     = queryFrequency()
)

func queryFrequency() int64 {
	var f int64
	if r, _, _ := procFrequency.Call(uintptr(unsafe.Pointer(&f))); r == 0 || f <= 0 {
		return 0
	}
	return f
}

func counter() int64 {
	var c int64
	if frequency == 0 {
		return 0
	}
	if r, _, _ := procCounter.Call(uintptr(unsafe.Pointer(&c))); r == 0 {
		return 0
	}
	return c
}

func since(s Stamp) time.Duration {
	if now := counter(); now != 0 && s.tick != 0 {
		elapsed := now - s.tick
		// split the multiplication so a long interval cannot overflow
		secs, rem := elapsed/frequency, elapsed%frequency
		return time.Duration(secs)*time.Second + time.Duration(rem*int64(time.Second)/frequency)
	}
	return time.Since(s.wall)
}
