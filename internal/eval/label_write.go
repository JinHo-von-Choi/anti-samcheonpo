package eval

import (
	"os"
	"path/filepath"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/procgroup"
)

// WriteLabelFile publishes a complete private snapshot. Callers hold
// LockLabels across reading the previous snapshot and replacing it.
func WriteLabelFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".review-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return procgroup.Rename(name, path)
}
