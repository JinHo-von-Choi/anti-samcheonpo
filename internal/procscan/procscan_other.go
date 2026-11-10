//go:build !linux

package procscan

import "time"

// Leftover is not implemented outside Linux: it reports nothing.
func Leftover(root string, windows []Window, exclude map[int]bool, host int, deadline time.Time) []Proc {
	return nil
}

// HostPID is not implemented outside Linux: it reports nothing.
func HostPID(hook int) int { return 0 }
