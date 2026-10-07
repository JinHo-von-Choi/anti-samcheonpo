package live

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// skipDirs are never walked or watched.
var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, "target": true, "dist": true, "build": true,
	".venv": true, "venv": true, "__pycache__": true, ".gradle": true, ".idea": true, ".next": true, ".samcheonpo": true, ".cache": true}

// Workspace computes live workspace fingerprints.
type Workspace struct {
	Root       string
	Scope      []string // glob prefixes used when the tree is too large
	isGit      bool
	mu         sync.Mutex
	computeMu  sync.Mutex
	cache      map[string]statEntry
	cur        string
	dirty      bool
	generation uint64
	watcher    *fsnotify.Watcher
	Warn       string
	// life is canceled by Close; running computations stop and Close waits.
	life context.Context
	stop context.CancelFunc
}

type statEntry struct {
	size  int64
	mtime int64
	hash  string
}

// MaxFiles is the non-git file limit before falling back to scope paths.
const MaxFiles = 50000

// NewWorkspace prepares a workspace for root.
func NewWorkspace(root string) *Workspace {
	w := &Workspace{Root: root, cache: map[string]statEntry{}, dirty: true}
	w.life, w.stop = context.WithCancel(context.Background())
	if out, err := exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree").Output(); err == nil && strings.TrimSpace(string(out)) == "true" {
		w.isGit = true
	}
	return w
}

// Current returns the last computed fingerprint and whether it is fresh.
func (w *Workspace) Current() (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cur, !w.dirty && w.cur != ""
}

// Generation counts observed changes; a read is consistent only if it is the
// same before and after.
func (w *Workspace) Generation() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.generation
}

// Invalidate marks the fingerprint stale.
func (w *Workspace) Invalidate() {
	w.mu.Lock()
	w.dirty = true
	w.generation++
	w.mu.Unlock()
}

func (w *Workspace) SetScope(scope []string) {
	w.mu.Lock()
	if !slices.Equal(w.Scope, scope) {
		w.Scope = append([]string(nil), scope...)
		w.dirty = true
		w.generation++
	}
	w.mu.Unlock()
}

// Compute recomputes the fingerprint.
func (w *Workspace) Compute(ctx context.Context) (string, error) {
	w.computeMu.Lock()
	defer w.computeMu.Unlock()
	if w.life.Err() != nil {
		return "", w.life.Err()
	}
	w.mu.Lock()
	generation := w.generation
	w.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-w.life.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	var h string
	var err error
	if w.isGit {
		h, err = w.gitTree(ctx)
	} else {
		h, err = w.walk(ctx)
	}
	if err != nil {
		return "", err
	}
	w.mu.Lock()
	w.cur, w.dirty = h, generation != w.generation
	w.mu.Unlock()
	return h, nil
}

// gitTree hashes the working tree through a temporary index so the user's
// index is untouched; the copied stat cache means only changed files are read.
func (w *Workspace) gitTree(ctx context.Context) (string, error) {
	gitDir, err := exec.CommandContext(ctx, "git", "-C", w.Root, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return "", err
	}
	idx := filepath.Join(strings.TrimSpace(string(gitDir)), "index")
	tmp, err := os.CreateTemp("", "samcheonpo-index-")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if src, err := os.Open(idx); err == nil {
		_, _ = io.Copy(tmp, src)
		src.Close()
		tmp.Close()
	} else {
		// no index yet (fresh repository): let git start an empty one
		tmp.Close()
		os.Remove(tmpName)
	}
	env := append(os.Environ(), "GIT_INDEX_FILE="+tmpName, "GIT_OPTIONAL_LOCKS=0")
	add := exec.CommandContext(ctx, "git", "-C", w.Root, "add", "-A", "--", ".", ":(exclude).samcheonpo")
	add.Env = env
	if out, err := add.CombinedOutput(); err != nil {
		return "", &gitErr{string(out), err}
	}
	wt := exec.CommandContext(ctx, "git", "-C", w.Root, "write-tree")
	wt.Env = env
	out, err := wt.Output()
	if err != nil {
		return "", err
	}
	return "git:" + strings.TrimSpace(string(out)), nil
}

type gitErr struct {
	out string
	err error
}

func (g *gitErr) Error() string { return g.err.Error() + ": " + strings.TrimSpace(g.out) }

// walk hashes (path, size, content) for every file, reusing content hashes of
// files whose size and mtime did not change.
func (w *Workspace) walk(ctx context.Context) (string, error) {
	var files []string
	tooMany := false
	err := filepath.WalkDir(w.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != w.Root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		files = append(files, p)
		if len(files) > MaxFiles {
			tooMany = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if tooMany {
		w.Warn = "파일이 5만 개를 넘어 계약 범위 경로만 지문에 넣는다"
		files = w.scopeFiles()
	}
	sort.Strings(files)
	h := sha256.New()
	seen := map[string]bool{}
	for _, p := range files {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(w.Root, p)
		seen[rel] = true
		e, ok := w.cache[rel]
		if !ok || e.size != st.Size() || e.mtime != st.ModTime().UnixNano() {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			s := sha256.Sum256(b)
			e = statEntry{size: st.Size(), mtime: st.ModTime().UnixNano(), hash: hex.EncodeToString(s[:16])}
			w.cache[rel] = e
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write([]byte(e.hash))
		h.Write([]byte{'\n'})
	}
	for k := range w.cache {
		if !seen[k] {
			delete(w.cache, k)
		}
	}
	return "fs:" + hex.EncodeToString(h.Sum(nil)[:16]), nil
}

func (w *Workspace) scopeFiles() []string {
	var out []string
	w.mu.Lock()
	scope := append([]string(nil), w.Scope...)
	w.mu.Unlock()
	for _, g := range scope {
		base := filepath.Join(w.Root, strings.TrimSuffix(strings.TrimSuffix(g, "/**"), "/*"))
		_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				out = append(out, p)
			}
			return nil
		})
	}
	return out
}

// Watch starts fsnotify watching; changes invalidate the fingerprint. The
// number of watched directories is bounded to stay within inotify limits.
func (w *Workspace) Watch(maxDirs int) error {
	wt, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.watcher = wt
	n := 0
	_ = filepath.WalkDir(w.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if p != w.Root && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if n >= maxDirs {
			return filepath.SkipAll
		}
		if wt.Add(p) == nil {
			n++
		}
		return nil
	})
	go func() {
		for {
			select {
			case ev, ok := <-wt.Events:
				if !ok {
					return
				}
				if strings.Contains(ev.Name, string(filepath.Separator)+".git"+string(filepath.Separator)) {
					continue
				}
				w.Invalidate()
				if ev.Op&fsnotify.Create != 0 {
					if st, err := os.Stat(ev.Name); err == nil && st.IsDir() && !skipDirs[filepath.Base(ev.Name)] {
						_ = wt.Add(ev.Name)
					}
				}
			case _, ok := <-wt.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return nil
}

// Close stops watching, cancels running computations and waits for them.
func (w *Workspace) Close() {
	w.stop()
	w.computeMu.Lock()
	defer w.computeMu.Unlock()
	if w.watcher != nil {
		w.watcher.Close()
	}
}

// IsGit reports whether the workspace is a git work tree.
func (w *Workspace) IsGit() bool { return w.isGit }

// gitStashCommit returns a commit containing the current tracked changes
// (HEAD when clean), used for isolated checkpoint worktrees.
func gitStashCommit(ctx context.Context, root string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "stash", "create").Output()
	if err != nil {
		return "", err
	}
	c := strings.TrimSpace(string(out))
	if c == "" {
		out, err = exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD").Output()
		if err != nil {
			return "", err
		}
		c = strings.TrimSpace(string(out))
	}
	return c, nil
}

func untracked(ctx context.Context, root string) []string {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return nil
	}
	var res []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			res = append(res, string(p))
		}
	}
	return res
}

func withTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
