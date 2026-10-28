package hostlimit

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Disk is the space of the file system that holds a path.
type Disk struct {
	Free, Total uint64 // bytes available to the user and the whole size
	Known       bool
}

// FreeRatio is the share of the file system still free (0 when unknown).
func (d Disk) FreeRatio() float64 {
	if !d.Known || d.Total == 0 {
		return 0
	}
	return float64(d.Free) / float64(d.Total)
}

// ErrBudget is returned when a measurement stopped at its time or entry limit.
var ErrBudget = errors.New("measurement budget spent")

// Occupied sums the disk blocks the files under dirs take, below root. It
// stops at the deadline or after maxEntries entries and then reports
// ErrBudget with what it summed so far: a partial number, never a full one.
// A missing directory adds nothing.
func Occupied(root string, dirs []string, deadline time.Time, maxEntries int) (int64, error) {
	var total int64
	n := 0
	for _, d := range dirs {
		p := filepath.Join(root, d)
		if st, err := os.Lstat(p); err != nil || !st.IsDir() {
			continue
		}
		err := filepath.WalkDir(p, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable entries add nothing
			}
			n++
			if n > maxEntries || n%1024 == 0 && time.Now().After(deadline) {
				return ErrBudget
			}
			if e.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			info, err := e.Info()
			if err != nil {
				return nil
			}
			total += blocks(info)
			return nil
		})
		if errors.Is(err, ErrBudget) {
			return total, ErrBudget
		}
	}
	return total, nil
}
