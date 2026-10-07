//go:build !linux

package bench

// stopVerifiedDaemon has no way to confirm the pid's identity without /proc,
// so it never signals and the monitoring total stays unconfirmed.
func stopVerifiedDaemon(int, string) bool { return false }
