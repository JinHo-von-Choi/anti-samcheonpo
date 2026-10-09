//go:build !windows

package procgroup

// RetryTransient runs op once: Unix has no sharing violations to wait out.
func RetryTransient(op func() error) error { return op() }
