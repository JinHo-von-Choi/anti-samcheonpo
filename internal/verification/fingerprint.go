package verification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Fingerprint reads literal files/directories without following links. It
// fails closed on missing inputs, unreadable files, excessive work or a file
// changing while read. Paths and contents are hashed, not retained.
func Fingerprint(ctx context.Context, root string, paths []string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("no observed inputs")
	}
	paths = append([]string(nil), paths...)
	sort.Strings(paths)
	h := sha256.New()
	files := 0
	var bytesRead int64
	for _, p := range paths {
		if p == "" {
			return "", fmt.Errorf("empty input path")
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		// WalkDir does not follow terminal links, but parent components also
		// need validation or a literal file can escape through a linked dir.
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			return "", err
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		if resolved != abs {
			return "", fmt.Errorf("symbolic input path is not reusable")
		}
		err = filepath.WalkDir(p, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			files++
			if files > 10000 {
				return fmt.Errorf("input fingerprint file limit exceeded")
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "%d:%s:%s:", len(path), path, info.Mode())
			if info.IsDir() {
				return nil
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("non-regular input")
			}
			bytesRead += info.Size()
			if bytesRead > 64<<20 {
				return fmt.Errorf("input fingerprint byte limit exceeded")
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			before, err := f.Stat()
			if err != nil {
				f.Close()
				return err
			}
			if !os.SameFile(info, before) {
				f.Close()
				return fmt.Errorf("input replaced while opening")
			}
			fmt.Fprintf(h, "%d:", before.Size())
			n, err := io.Copy(h, io.LimitReader(f, info.Size()+1))
			after, statErr := f.Stat()
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if statErr != nil {
				return statErr
			}
			if closeErr != nil {
				return closeErr
			}
			if n != before.Size() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
				return fmt.Errorf("input changed while reading")
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
