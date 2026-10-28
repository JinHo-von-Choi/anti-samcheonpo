//go:build !linux

package procscan

import "time"

// Leftover is not implemented outside Linux: it reports nothing.
func Leftover(root string, windows []Window, exclude map[int]bool, deadline time.Time) []Proc {
	return nil
}
