package bench

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// extractTar unpacks a tar stream (the output of `git archive`) below dir. It
// replaces an external tar so that extraction means the same on every system:
// entries that would land outside dir are refused, and a symbolic link is
// created only when the system lets this process make one.
func extractTar(r io.Reader, dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := path.Clean(h.Name)
		if name == "." {
			continue
		}
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("보관본 항목이 폴더 밖을 가리킨다: %q", h.Name)
		}
		local := filepath.FromSlash(name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(local, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := root.MkdirAll(filepath.Dir(local), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := root.OpenFile(local, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := root.MkdirAll(filepath.Dir(local), 0o755); err != nil {
				return err
			}
			if err := root.Symlink(h.Linkname, local); err != nil {
				return fmt.Errorf("심볼릭 링크 %q를 만들 수 없다: %w", h.Name, err)
			}
		default:
			return fmt.Errorf("보관본 항목 %q의 종류(%c)를 풀 수 없다", h.Name, h.Typeflag)
		}
	}
}
