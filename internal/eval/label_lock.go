package eval

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/procgroup"
)

var ErrLabelsBusy = errors.New("다른 검토 저장이 진행 중입니다. 다시 저장해 주세요")

// LockLabels protects read/check/write transactions across CLI and web
// processes. The lock file stays in place; the OS releases ownership on exit.
func LockLabels(root string) (func(), error) {
	if err := procgroup.MkdirAllPrivate(root); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(root, ".label.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockLabelFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}
