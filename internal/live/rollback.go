package live

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Files larger than this, or beyond the session total, are not kept: the
// rollback reports them as untracked instead of restoring a partial copy.
const (
	maxTrackedFile  = 1 << 20
	maxTrackedTotal = 64 << 20
)

// fileState is a file's content at one point; absent means it did not exist.
type fileState struct {
	data   []byte
	absent bool
}

func (f fileState) hash() string {
	if f.absent {
		return ""
	}
	h := sha256.Sum256(f.data)
	return hex.EncodeToString(h[:])
}

// rollbackState keeps what is needed to undo the agent's file writes back to
// the last confirmed progress. It covers files changed through write/edit
// tools only; a shell command that rewrites files is not tracked.
type rollbackState struct {
	baseline    map[string]fileState // before the agent's first write
	snapshot    map[string]fileState // at the last confirmed progress
	snapshotSeq int64
	agentHash   map[string]string // what the agent's last write left
	untracked   map[string]string // path -> why it cannot be restored
	bytes       int64
}

func (r *rollbackState) init() {
	if r.baseline == nil {
		r.baseline, r.snapshot, r.agentHash, r.untracked = map[string]fileState{}, map[string]fileState{}, map[string]string{}, map[string]string{}
		r.snapshotSeq = -1
	}
}

// rollbackPath returns the absolute path of a project file, or "" when the
// path lies outside the project.
func (s *Session) rollbackPath(p string) string {
	if p == "" {
		return ""
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(s.Root, p)
	}
	rel, err := filepath.Rel(s.Root, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return abs
}

func readState(abs string) (fileState, error) {
	st, err := os.Lstat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return fileState{absent: true}, nil
	}
	if err != nil {
		return fileState{}, err
	}
	if !st.Mode().IsRegular() {
		return fileState{}, fmt.Errorf("일반 파일이 아님")
	}
	if st.Size() > maxTrackedFile {
		return fileState{}, fmt.Errorf("파일이 %dMB보다 큼", maxTrackedFile>>20)
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return fileState{}, err
	}
	return fileState{data: b}, nil
}

// keep stores a state within the session budget; called with s.mu held.
func (s *Session) keep(m map[string]fileState, rel string, f fileState) bool {
	r := &s.rb
	if old, ok := m[rel]; ok {
		r.bytes -= int64(len(old.data))
	}
	if r.bytes+int64(len(f.data)) > maxTrackedTotal {
		r.untracked[rel] = "보관 용량 초과"
		delete(m, rel)
		return false
	}
	m[rel] = f
	r.bytes += int64(len(f.data))
	return true
}

// trackBefore records a file's content before the agent's first write to it.
// Called with s.mu held, before the write runs.
func (s *Session) trackBefore(paths []string) {
	s.rb.init()
	for _, p := range paths {
		abs := s.rollbackPath(p)
		if abs == "" {
			continue
		}
		rel, _ := filepath.Rel(s.Root, abs)
		if _, ok := s.rb.baseline[rel]; ok {
			continue
		}
		if _, ok := s.rb.untracked[rel]; ok {
			continue
		}
		f, err := readState(abs)
		if err != nil {
			s.rb.untracked[rel] = err.Error()
			continue
		}
		s.keep(s.rb.baseline, rel, f)
	}
}

// trackAfter records what the agent's write left on disk. Called with s.mu held.
func (s *Session) trackAfter(paths []string) {
	s.rb.init()
	for _, p := range paths {
		abs := s.rollbackPath(p)
		if abs == "" {
			continue
		}
		rel, _ := filepath.Rel(s.Root, abs)
		if _, ok := s.rb.baseline[rel]; !ok {
			continue
		}
		f, err := readState(abs)
		if err != nil {
			s.rb.untracked[rel] = err.Error()
			continue
		}
		s.rb.agentHash[rel] = f.hash()
	}
}

// snapshotProgress keeps the agent-touched files as they are at a progress
// point a passing check confirmed (estimated progress from writes alone is
// not a safe point to return to). Called with s.mu held.
func (s *Session) snapshotProgress(seq int64) {
	s.rb.init()
	for rel := range s.rb.agentHash {
		f, err := readState(filepath.Join(s.Root, rel))
		if err != nil {
			s.rb.untracked[rel] = err.Error()
			continue
		}
		s.keep(s.rb.snapshot, rel, f)
	}
	s.rb.snapshotSeq = seq
}

type rollbackItem struct {
	Path   string `json:"path"`
	Action string `json:"action"` // restore | delete
	Reason string `json:"reason,omitempty"`
	target fileState
}

// rollbackPlan lists what a rollback would do. Called with s.mu held.
func (s *Session) rollbackPlan() (todo, skipped []rollbackItem) {
	s.rb.init()
	var paths []string
	for rel := range s.rb.agentHash {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		if why, ok := s.rb.untracked[rel]; ok {
			skipped = append(skipped, rollbackItem{Path: rel, Reason: why})
			continue
		}
		target, ok := s.rb.snapshot[rel]
		if !ok {
			target, ok = s.rb.baseline[rel]
		}
		if !ok {
			skipped = append(skipped, rollbackItem{Path: rel, Reason: "되돌릴 기준 내용이 없음"})
			continue
		}
		cur, err := readState(filepath.Join(s.Root, rel))
		if err != nil {
			skipped = append(skipped, rollbackItem{Path: rel, Reason: err.Error()})
			continue
		}
		if cur.hash() == target.hash() && cur.absent == target.absent {
			continue
		}
		if cur.hash() != s.rb.agentHash[rel] || (cur.absent && s.rb.agentHash[rel] != "") {
			skipped = append(skipped, rollbackItem{Path: rel, Reason: "AI가 마지막으로 쓴 뒤 다른 곳에서 바뀌어 덮어쓰지 않음"})
			continue
		}
		action := "restore"
		if target.absent {
			action = "delete"
		}
		todo = append(todo, rollbackItem{Path: rel, Action: action, target: target})
	}
	return todo, skipped
}

func (s *Session) rollbackBasis() string {
	if s.rb.snapshotSeq >= 0 {
		return fmt.Sprintf("마지막으로 진척이 확인된 시점(이벤트 %d)", s.rb.snapshotSeq)
	}
	return "AI가 이 세션에서 처음 쓰기 전 상태(아직 확인된 진척 없음)"
}

// rollback previews or applies the rollback. Called without s.mu.
func (s *Session) rollback(apply bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	todo, skipped := s.rollbackPlan()
	var b strings.Builder
	fmt.Fprintf(&b, "되돌리기 기준: %s\n", s.rollbackBasis())
	if len(todo) == 0 {
		b.WriteString("되돌릴 파일이 없다.\n")
	}
	for _, it := range todo {
		verb := "이전 내용으로 복구"
		if it.Action == "delete" {
			verb = "AI가 새로 만든 파일이라 삭제"
		}
		fmt.Fprintf(&b, "- %s: %s\n", it.Path, verb)
	}
	for _, it := range skipped {
		fmt.Fprintf(&b, "- %s: 건너뜀 (%s)\n", it.Path, it.Reason)
	}
	b.WriteString("셸 명령으로 바뀐 파일은 대상이 아니다.\n")
	if !apply {
		if len(todo) > 0 {
			b.WriteString("미리보기다. 실제로 되돌리려면 /samcheonpo:rollback apply 를 실행한다.\n")
		}
		return b.String(), nil
	}
	var done []rollbackItem
	var failed []string
	for _, it := range todo {
		abs := filepath.Join(s.Root, it.Path)
		var err error
		if it.Action == "delete" {
			err = os.Remove(abs)
		} else {
			err = writeAtomic(abs, it.target.data)
		}
		if err != nil {
			failed = append(failed, it.Path+": "+err.Error())
			continue
		}
		s.rb.agentHash[it.Path] = it.target.hash()
		done = append(done, it)
	}
	if s.ws != nil {
		s.ws.Invalidate()
	}
	if s.db != nil {
		restored, _ := json.Marshal(done)
		skip, _ := json.Marshal(skipped)
		_, err := s.db.Exec(`INSERT INTO rollback(session_id, at, basis_seq, restored, skipped, failed) VALUES (?,?,?,?,?,?)`,
			s.ID, time.Now().UTC().Format(time.RFC3339Nano), s.rb.snapshotSeq, string(restored), string(skip), strings.Join(failed, "\n"))
		s.recordStorageError(err)
	}
	fmt.Fprintf(&b, "%d개 파일을 되돌렸다.\n", len(done))
	for _, f := range failed {
		fmt.Fprintf(&b, "실패: %s\n", f)
	}
	return b.String(), nil
}

func writeAtomic(abs string, data []byte) error {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(abs); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(abs), ".samcheonpo-rollback-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, abs)
}
