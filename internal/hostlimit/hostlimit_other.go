//go:build !linux

package hostlimit

// Inotify reports no budget on hosts that do not expose one. The count is
// unknown rather than zero, so a caller cannot mistake it for headroom.
func Inotify() Watch { return Watch{} }

// InotifyLimit reports no limit on hosts that do not expose one.
func InotifyLimit() (int, bool) { return 0, false }
