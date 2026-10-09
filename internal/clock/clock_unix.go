//go:build !windows

package clock

import "time"

func counter() int64 { return 0 }

func since(s Stamp) time.Duration { return time.Since(s.wall) }
