package install

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func generatedFiles(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("일반 파일이 아닌 플러그인 경로: %s", path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[rel] = fmt.Sprintf("%x", sha256.Sum256(b))
		return nil
	})
	return files, err
}

// Remove only unchanged files we created. Never follow a replaced directory
// symlink or remove a user-added file. Legacy manifests conservatively retain.
func removeGenerated(root string, files map[string]string, out func(string)) error {
	if len(files) == 0 {
		out("생성 파일 기록이 없어 플러그인 파일을 보존했다")
		return nil
	}
	rootInfo, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !rootInfo.IsDir() {
		out("교체된 플러그인 경로를 보존했다")
		return nil
	}
	dirs := map[string]bool{root: true}
	preserved := false
	for rel, expected := range files {
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("잘못된 생성 파일 경로")
		}
		path := filepath.Join(root, rel)
		safe := true
		var parents []string
		for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
			st, err := os.Lstat(parent)
			if os.IsNotExist(err) {
				safe = false
				break
			}
			if err != nil {
				return err
			}
			if !st.IsDir() {
				safe = false
				preserved = true
				break
			}
			parents = append(parents, parent)
			if parent == root {
				break
			}
		}
		if !safe {
			continue
		}
		for _, parent := range parents {
			dirs[parent] = true
		}
		st, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			preserved = true
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(b)) != expected {
			preserved = true
			continue
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	ordered := make([]string, 0, len(dirs))
	for dir := range dirs {
		ordered = append(ordered, dir)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return strings.Count(ordered[i], string(filepath.Separator)) > strings.Count(ordered[j], string(filepath.Separator))
	})
	for _, dir := range ordered {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			preserved = true
			continue
		}
		if len(entries) > 0 {
			preserved = true
			continue
		}
		if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if preserved {
		out("사용자가 추가·수정한 플러그인 파일은 보존했다")
	}
	return nil
}
