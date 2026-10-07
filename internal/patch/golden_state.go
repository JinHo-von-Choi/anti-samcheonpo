// Package patch keeps durable records of the project at the points a check
// passed, so a later rollback can return to a state that was verified rather
// than one inferred from writes.
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

// maxSnapshotBytes bounds one capture. A snapshot holds the files the agent
// owns at a passing point, not the project; a capture over this size is a
// sign that ownership tracking went wrong, and it is refused whole.
const maxSnapshotBytes = 64 << 20

// Snapshot paths inside the project, relative to the work directory.
const (
	snapshotsDir = ".samcheonpo/snapshots"
	metaName     = "meta.json"
	filesDir     = "files"
	// tmpPrefix marks a capture that is still being written. Such a directory
	// is never listed, read or trimmed: only the rename below publishes a
	// snapshot, so a capture that dies halfway leaves no half-restorable point.
	tmpPrefix = ".tmp-"
)

// ErrSnapshotGone is returned when the id names no snapshot that can be read:
// it was never captured, it was already trimmed away, or it is not a snapshot
// directory at all. Concurrent captures can trim a snapshot between the moment
// a caller reads LatestSnapshotID and the moment it reads it, so callers that
// race for the newest point should expect this one.
var ErrSnapshotGone = errors.New("스냅샷이 없거나 정리되어 읽을 수 없음")

// ErrSnapshotTooLarge is returned when a capture would go over maxSnapshotBytes.
var ErrSnapshotTooLarge = errors.New("스냅샷이 너무 커서 기록하지 않음")

// File is one captured file: its bytes and permission bits.
type File struct {
	Data []byte
	Mode fs.FileMode
}

// SnapshotMeta describes one passing point.
type SnapshotMeta struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Reason    string    `json:"reason"`
	// Session and Seq name the session event that confirmed the pass.
	Session string `json:"session,omitempty"`
	Seq     int64  `json:"seq,omitempty"`
	// Tree is the workspace fingerprint at the pass (a git tree hash when the
	// project is a repository), so the whole state can be found again.
	Tree string `json:"tree,omitempty"`
	// Files are the project-relative slash paths captured, sorted. Each one is
	// stored under files/ inside the snapshot directory, byte for byte with its
	// permission bits.
	Files []string `json:"files"`
	// Hashes are the content hashes of Files as the agent left them, keyed by
	// path, so a later session can tell whether a file still holds the agent's
	// own content.
	Hashes map[string]string `json:"hashes,omitempty"`
}

// GoldenStateManager keeps the last few states the agent's files were in when
// a check passed. All its methods are safe for concurrent use.
type GoldenStateManager struct {
	workDir string
	mu      sync.Mutex
}

// NewGoldenStateManager returns a manager for the project at workDir. The
// snapshot directory is created on the first capture, so a project with no
// passing check yet leaves no trace.
func NewGoldenStateManager(workDir string) *GoldenStateManager {
	return &GoldenStateManager{workDir: workDir}
}

// WorkDir returns the project directory snapshots are taken in.
func (g *GoldenStateManager) WorkDir() string { return g.workDir }

// Capture writes the given files as a new snapshot and returns its id. meta
// supplies the reason, session, sequence and tree fingerprint; its ID,
// Timestamp, Files and Hashes are set here. Paths that leave the project are
// refused, at most maxSnapshots snapshots survive, and when trimming fails the
// snapshot id is still returned with the error.
func (g *GoldenStateManager) Capture(meta SnapshotMeta, files map[string]File) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var total int64
	for rel, f := range files {
		if rel == "" || filepath.IsAbs(filepath.FromSlash(rel)) || !safeRelPath(filepath.FromSlash(rel)) {
			return "", fmt.Errorf("%s: 프로젝트 밖을 가리켜 기록하지 않음", rel)
		}
		total += int64(len(f.Data))
	}
	if total > maxSnapshotBytes {
		return "", ErrSnapshotTooLarge
	}
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
	meta.ID, meta.Timestamp = id, now
	meta.Files, meta.Hashes = nil, map[string]string{}
	for rel, f := range files {
		name := filepath.FromSlash(rel)
		out := filepath.Join(tmp, filesDir, name)
		if err := root.MkdirAll(filepath.Dir(out), 0o700); err != nil {
			_ = root.RemoveAll(tmp)
			return "", err
		}
		mode := f.Mode.Perm()
		if mode == 0 {
			mode = 0o644
		}
		if err := root.WriteFile(out, f.Data, mode); err != nil {
			_ = root.RemoveAll(tmp)
			return "", fmt.Errorf("%s를 스냅샷에 넣지 못함: %w", rel, err)
		}
		meta.Files = append(meta.Files, rel)
		meta.Hashes[rel] = HashContent(string(f.Data))
	}
	slices.Sort(meta.Files)
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

// Get returns the metadata of one snapshot.
func (g *GoldenStateManager) Get(id string) (SnapshotMeta, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	root, err := os.OpenRoot(g.workDir)
	if err != nil {
		return SnapshotMeta{}, fmt.Errorf("프로젝트 폴더를 열 수 없음(%s): %w", g.workDir, err)
	}
	defer root.Close()
	return readMeta(root, id)
}

// ReadFile returns one captured file of a snapshot.
func (g *GoldenStateManager) ReadFile(id, rel string) (File, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	root, err := os.OpenRoot(g.workDir)
	if err != nil {
		return File{}, fmt.Errorf("프로젝트 폴더를 열 수 없음(%s): %w", g.workDir, err)
	}
	defer root.Close()
	meta, err := readMeta(root, id)
	if err != nil {
		return File{}, err
	}
	if !slices.Contains(meta.Files, rel) {
		return File{}, fmt.Errorf("%w: %s에 %s가 없음", ErrSnapshotGone, id, rel)
	}
	name := filepath.Join(snapshotsDir, id, filesDir, filepath.FromSlash(rel))
	data, err := root.ReadFile(name)
	if err != nil {
		return File{}, fmt.Errorf("%s: 스냅샷에 내용이 없음: %w", rel, err)
	}
	f := File{Data: data, Mode: 0o644}
	if st, err := root.Stat(name); err == nil {
		f.Mode = st.Mode().Perm()
	}
	return f, nil
}

// LatestSnapshotID returns the id of the most recent snapshot, or "" when
// nothing has been captured.
func (g *GoldenStateManager) LatestSnapshotID() string {
	list := g.ListSnapshots()
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

func readMeta(root *os.Root, id string) (SnapshotMeta, error) {
	if !validSnapshotID(id) {
		return SnapshotMeta{}, fmt.Errorf("%w: %q", ErrSnapshotGone, id)
	}
	b, err := root.ReadFile(filepath.Join(snapshotsDir, id, metaName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return SnapshotMeta{}, fmt.Errorf("%w: %s", ErrSnapshotGone, id)
		}
		return SnapshotMeta{}, fmt.Errorf("스냅샷 %s를 읽을 수 없음: %w", id, err)
	}
	var meta SnapshotMeta
	if err := json.Unmarshal(b, &meta); err != nil || meta.ID != id {
		return SnapshotMeta{}, fmt.Errorf("스냅샷 %s의 정보가 깨져 있음", id)
	}
	return meta, nil
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
		meta, err := readMeta(root, e.Name())
		if err != nil {
			continue
		}
		list = append(list, meta)
	}
	slices.SortFunc(list, func(a, b SnapshotMeta) int { return strings.Compare(b.ID, a.ID) })
	return list
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
	if name == "" || filepath.IsAbs(name) || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") || filepath.VolumeName(name) != "" {
		return false
	}
	clean := filepath.Clean(name)
	switch clean {
	case ".", "..":
		return false
	}
	return !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
