package patch

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// maxSnapshots is how many passing points are kept. The oldest is dropped
// when a capture would go over it.
const maxSnapshots = 5

// Snapshot paths inside the project, relative to the work directory.
const (
	snapshotsDir = ".samcheonpo/snapshots"
	metaName     = "meta.json"
	filesDir     = "files"
	// tmpPrefix marks a capture that is still being written. Such a directory
	// is never listed, restored or trimmed: only the rename below publishes a
	// snapshot, so a capture that dies halfway leaves no half-restorable point.
	tmpPrefix = ".tmp-"
)

// snapshotSkipDirs are never copied: version control data, dependency trees
// and build output are not the agent's work, they are large, and they are
// either recoverable on their own or never part of what a check accepted.
var snapshotSkipDirs = map[string]bool{".git": true, ".svn": true, ".hg": true, "node_modules": true,
	"vendor": true, "target": true, "dist": true, "build": true, "out": true,
	".venv": true, "venv": true, "__pycache__": true, ".gradle": true, ".idea": true,
	".next": true, ".nuxt": true, ".cache": true, ".samcheonpo": true, ".unlazy": true, ".serena": true}

// ErrSnapshotGone is returned when the id names no snapshot that can be
// restored: it was never captured, it was already trimmed away, or it is not
// a snapshot directory at all. Concurrent captures can trim a snapshot between
// the moment a caller reads LatestSnapshotID and the moment it restores it,
// so callers that race for the newest point should expect this one.
var ErrSnapshotGone = errors.New("스냅샷이 없거나 정리되어 복원할 수 없음")

// SnapshotMeta describes one passing point.
type SnapshotMeta struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Reason    string    `json:"reason"`
	// Files are the project-relative slash paths captured, sorted. Each one is
	// stored under files/ inside the snapshot directory, byte for byte with its
	// permission bits.
	Files []string `json:"files"`
}

// GoldenStateManager keeps the last few states the project was in when every
// check passed, so a later failure can be rolled back to a point that was
// actually verified instead of one inferred from writes. All its methods are
// safe for concurrent use.
type GoldenStateManager struct {
	workDir string
	mu      sync.Mutex
}

// NewGoldenStateManager returns a manager for the project at workDir. The
// snapshot directory is created on the first capture, so a project with no
// passing check yet leaves no trace.
func NewGoldenStateManager(workDir string) *GoldenStateManager {
	return &GoldenStateManager{workDir: filepath.Clean(workDir)}
}

// WorkDir returns the project directory snapshots are taken in.
func (g *GoldenStateManager) WorkDir() string { return g.workDir }

// CapturePassState copies the current state of the project into a new
// snapshot and returns its id. reason says which checks passed. Directories
// that hold large or tool-internal data are left out, links and special files
// are left out, and at most maxSnapshots snapshots survive; when trimming
// fails the snapshot id is still returned with the error.
func (g *GoldenStateManager) CapturePassState(reason string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	root, err := os.OpenRoot(g.workDir)
	if err != nil {
		return "", fmt.Errorf("프로젝트 폴더를 열 수 없음(%s): %w", g.workDir, err)
	}
	defer root.Close()

	now := time.Now().UTC()
	id := newSnapshotID(now)
	tmp := filepath.Join(snapshotsDir, tmpPrefix+id)
	if err := root.MkdirAll(filepath.Join(tmp, filesDir), 0o700); err != nil {
		return "", fmt.Errorf("스냅샷 폴더를 만들 수 없음: %w", err)
	}
	files, err := captureTree(root, tmp)
	if err != nil {
		_ = root.RemoveAll(tmp)
		return "", err
	}
	meta := SnapshotMeta{ID: id, Timestamp: now, Reason: reason, Files: files}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		_ = root.RemoveAll(tmp)
		return "", fmt.Errorf("스냅샷 정보를 쓸 수 없음: %w", err)
	}
	if err := root.WriteFile(filepath.Join(tmp, metaName), append(b, '\n'), 0o600); err != nil {
		_ = root.RemoveAll(tmp)
		return "", fmt.Errorf("스냅샷 정보를 쓸 수 없음: %w", err)
	}
	if err := root.Rename(tmp, filepath.Join(snapshotsDir, id)); err != nil {
		_ = root.RemoveAll(tmp)
		return "", fmt.Errorf("스냅샷을 확정할 수 없음: %w", err)
	}
	if err := g.trim(root); err != nil {
		return id, err
	}
	return id, nil
}

// RestoreState puts every file of the snapshot back as it was captured.
// Files created after the capture are kept: a snapshot lists what it saw, so
// it cannot prove who owns a file it never held, and deleting a person's new
// work is worse than leaving it. Files that are gone, shortened or overwritten
// are written back whole; each write is atomic and stays inside the project.
func (g *GoldenStateManager) RestoreState(snapshotID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !validSnapshotID(snapshotID) {
		return fmt.Errorf("%w: %q", ErrSnapshotGone, snapshotID)
	}
	root, err := os.OpenRoot(g.workDir)
	if err != nil {
		return fmt.Errorf("프로젝트 폴더를 열 수 없음(%s): %w", g.workDir, err)
	}
	defer root.Close()

	dir := filepath.Join(snapshotsDir, snapshotID)
	b, err := root.ReadFile(filepath.Join(dir, metaName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrSnapshotGone, snapshotID)
		}
		return fmt.Errorf("스냅샷 %s를 읽을 수 없음: %w", snapshotID, err)
	}
	var meta SnapshotMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return fmt.Errorf("스냅샷 %s의 정보가 깨져 있음: %w", snapshotID, err)
	}
	if len(meta.Files) == 0 {
		return nil
	}

	var failed []error
	for _, rel := range meta.Files {
		name := filepath.FromSlash(rel)
		if rel == "" || filepath.IsAbs(name) || !safeRelPath(name) {
			failed = append(failed, fmt.Errorf("%s: 프로젝트 밖을 가리켜 복원하지 않음", rel))
			continue
		}
		// A link put in the file's place is never written through: it could
		// point outside the project, and the snapshot does not own it.
		if st, err := root.Lstat(name); err == nil && st.Mode()&fs.ModeSymlink != 0 {
			failed = append(failed, fmt.Errorf("%s: 지금 심볼릭 링크라 덮어쓰지 않음", rel))
			continue
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			failed = append(failed, fmt.Errorf("%s: %w", rel, err))
			continue
		}
		data, err := root.ReadFile(filepath.Join(dir, filesDir, name))
		if err != nil {
			failed = append(failed, fmt.Errorf("%s: 스냅샷에 내용이 없음: %w", rel, err))
			continue
		}
		mode := fs.FileMode(0o644)
		if st, err := root.Stat(filepath.Join(dir, filesDir, name)); err == nil {
			mode = st.Mode().Perm()
		}
		if err := writeInRoot(root, name, data, mode); err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", rel, err))
		}
	}
	return errors.Join(failed...)
}

// LatestSnapshotID returns the id of the most recent snapshot, or "" when
// nothing has been captured.
func (g *GoldenStateManager) LatestSnapshotID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	root, err := os.OpenRoot(g.workDir)
	if err != nil {
		return ""
	}
	defer root.Close()
	list := listSnapshots(root)
	if len(list) == 0 {
		return ""
	}
	return list[0].ID
}

// ListSnapshots returns the kept snapshots, newest first. A snapshot whose
// metadata cannot be read is not a snapshot and is left out rather than
// reported as an empty one.
func (g *GoldenStateManager) ListSnapshots() []SnapshotMeta {
	g.mu.Lock()
	defer g.mu.Unlock()
	root, err := os.OpenRoot(g.workDir)
	if err != nil {
		return nil
	}
	defer root.Close()
	return listSnapshots(root)
}

// trim drops the oldest snapshots beyond maxSnapshots.
func (g *GoldenStateManager) trim(root *os.Root) error {
	list := listSnapshots(root)
	var failed []error
	for i := maxSnapshots; i < len(list); i++ {
		if err := root.RemoveAll(filepath.Join(snapshotsDir, list[i].ID)); err != nil {
			failed = append(failed, fmt.Errorf("오래된 스냅샷 %s를 지우지 못함: %w", list[i].ID, err))
		}
	}
	return errors.Join(failed...)
}

// captureTree copies the project into dst/files and returns the copied paths.
func captureTree(root *os.Root, dst string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root.Name(), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root.Name(), p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if snapshotSkipDirs[name] {
				return fs.SkipDir
			}
			return nil
		}
		if snapshotSkipDirs[name] || d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, snapshotsDir+"/") {
			return nil
		}
		name = filepath.FromSlash(rel)
		st, err := root.Lstat(name)
		if err != nil {
			return fmt.Errorf("%s를 읽을 수 없음: %w", rel, err)
		}
		if !st.Mode().IsRegular() {
			return nil
		}
		data, err := root.ReadFile(name)
		if err != nil {
			return fmt.Errorf("%s를 읽을 수 없음: %w", rel, err)
		}
		out := filepath.Join(dst, filesDir, name)
		if err := root.MkdirAll(filepath.Dir(out), 0o700); err != nil {
			return err
		}
		if err := root.WriteFile(out, data, st.Mode().Perm()); err != nil {
			return fmt.Errorf("%s를 스냅샷에 넣지 못함: %w", rel, err)
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("프로젝트를 스냅샷에 넣지 못함: %w", err)
	}
	slices.Sort(files)
	return files, nil
}

// listSnapshots reads every snapshot directory, newest first. Ids start with a
// fixed-width UTC timestamp, so sorting by id is sorting by capture time.
func listSnapshots(root *os.Root) []SnapshotMeta {
	dir, err := root.Open(snapshotsDir)
	if err != nil {
		return nil
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil
	}
	var list []SnapshotMeta
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), tmpPrefix) {
			continue
		}
		b, err := root.ReadFile(filepath.Join(snapshotsDir, e.Name(), metaName))
		if err != nil {
			continue
		}
		var meta SnapshotMeta
		if json.Unmarshal(b, &meta) != nil || meta.ID != e.Name() {
			continue
		}
		list = append(list, meta)
	}
	slices.SortFunc(list, func(a, b SnapshotMeta) int { return strings.Compare(b.ID, a.ID) })
	return list
}

// writeInRoot replaces a project file atomically without leaving the root.
func writeInRoot(root *os.Root, rel string, data []byte, mode os.FileMode) error {
	if dir := filepath.Dir(rel); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(rel), ".samcheonpo-restore-"+hex.EncodeToString(suffix[:]))
	if err := root.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := root.Rename(tmp, rel); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

// newSnapshotID builds an id that sorts by capture time and stays unique when
// two captures land in the same nanosecond.
func newSnapshotID(now time.Time) string {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return now.Format("20060102T150405.000000000Z")
	}
	return now.Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(suffix[:])
}

// validSnapshotID keeps a snapshot id to a single directory name, so a path
// from the caller cannot reach outside the snapshot directory.
func validSnapshotID(id string) bool {
	if id == "" || strings.HasPrefix(id, tmpPrefix) || id == "." || id == ".." {
		return false
	}
	return safeRelPath(filepath.FromSlash(id)) && filepath.ToSlash(filepath.Clean(filepath.FromSlash(id))) == id
}

// safeRelPath reports whether a slash path stays inside its root.
func safeRelPath(name string) bool {
	if name == "" || filepath.IsAbs(name) {
		return false
	}
	clean := filepath.Clean(name)
	switch clean {
	case ".", "..":
		return false
	}
	return !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
