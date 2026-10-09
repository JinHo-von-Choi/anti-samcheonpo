//go:build !windows

package procgroup

// restrictToUser has nothing to add on Unix: MkdirAll created the directory
// with mode 0700.
func restrictToUser(string) error { return nil }
