package live

import (
	"crypto/rand"
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

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/procgroup"
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
	mode   os.FileMode
}

func (f fileState) hash() string {
	if f.absent {
		return ""
	}
	h := sha256.Sum256(f.data)
	return hex.EncodeToString(h[:])
}

// rollbackState keeps what is needed to undo the agent's file writes back to
// the last confirmed progress. It covers regular files changed through write
// and edit tools whose result could be reproduced from the tool input; shell
// commands, links, special files and anything whose ownership is unclear are
// listed as not covered and never written.
type rollbackState struct {
	baseline    map[string]fileState // before the agent's first write
	snapshot    map[string]fileState // at the last confirmed progress
	snapshotSeq int64
	agentHash   map[string]string            // what the agent's last write left
	untracked   map[string]string            // path -> why it cannot be restored
	expect      map[string]map[string]string // call -> path -> hash the tool input implies
	bytes       int64
}

func (r *rollbackState) init() {
	if r.baseline == nil {
		r.baseline, r.snapshot, r.agentHash, r.untracked = map[string]fileState{}, map[string]fileState{}, map[string]string{}, map[string]string{}
		r.expect = map[string]map[string]string{}
		r.snapshotSeq = -1
	}
}

// rollbackRel returns the project-relative slash path of p, or "" when p is
// outside the project.
func (s *Session) rollbackRel(p string) string {
	if p == "" {
		return ""
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(s.Root, p)
	}
	rel, err := filepath.Rel(s.Root, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// readState reads a project file through a root-confined handle. Links,
// hard-linked files and special files are refused: restoring them could
// change something outside the path the agent named.
func readState(root *os.Root, rel string) (fileState, error) {
	name := filepath.FromSlash(rel)
	st, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return fileState{absent: true}, nil
	}
	if err != nil {
		return fileState{}, err
	}
	switch {
	case st.Mode()&fs.ModeSymlink != 0:
		return fileState{}, fmt.Errorf("심볼릭 링크라 다루지 않음")
	case !st.Mode().IsRegular():
		return fileState{}, fmt.Errorf("일반 파일이 아님")
	case st.Size() > maxTrackedFile:
		return fileState{}, fmt.Errorf("파일이 %dMB보다 큼", maxTrackedFile>>20)
	}
	if procgroup.HardLinked(st) {
		return fileState{}, fmt.Errorf("하드링크라 다른 위치도 바뀔 수 있어 다루지 않음")
	}
	b, err := root.ReadFile(name)
	if err != nil {
		return fileState{}, err
	}
	return fileState{data: b, mode: st.Mode().Perm()}, nil
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

// expectedContent is the full content a Write, Edit or MultiEdit leaves in a
// file whose current state is cur. ok is false for anything it cannot
// reproduce exactly, so ownership is never guessed.
func expectedContent(tool string, input json.RawMessage, cur fileState) (string, bool) {
	type edit struct {
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	var p struct {
		Content    string `json:"content"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
		Edits      []edit `json:"edits"`
	}
	if json.Unmarshal(input, &p) != nil {
		return "", false
	}
	if tool == "Write" {
		return p.Content, true
	}
	if cur.absent {
		return "", false
	}
	text := string(cur.data)
	edits := p.Edits
	switch tool {
	case "Edit":
		edits = []edit{{p.OldString, p.NewString, p.ReplaceAll}}
	case "MultiEdit":
	default:
		return "", false
	}
	for _, e := range edits {
		if e.OldString == "" || !strings.Contains(text, e.OldString) {
			return "", false
		}
		if e.ReplaceAll {
			text = strings.ReplaceAll(text, e.OldString, e.NewString)
		} else {
			text = strings.Replace(text, e.OldString, e.NewString, 1)
		}
	}
	return text, true
}

func hashText(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// trackBefore runs before a write tool call, with s.mu held. It records the
// content before the agent's first write to a file, marks a file whose
// content changed since the agent's last write (someone else edited it), and
// remembers what the tool input says the file will contain.
func (s *Session) trackBefore(in HookInput, call string, paths []string) {
	s.rb.init()
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return
	}
	defer root.Close()
	for _, p := range paths {
		rel := s.rollbackRel(p)
		if rel == "" {
			continue
		}
		if _, ok := s.rb.untracked[rel]; ok {
			continue
		}
		cur, err := readState(root, rel)
		if err != nil {
			s.rb.untracked[rel] = err.Error()
			continue
		}
		if _, ok := s.rb.baseline[rel]; !ok {
			if !s.keep(s.rb.baseline, rel, cur) {
				continue
			}
		} else if h, ok := s.rb.agentHash[rel]; ok && h != cur.hash() {
			s.rb.untracked[rel] = "AI가 쓴 뒤 다른 편집이 섞여 소유권을 확정할 수 없음"
			continue
		}
		if text, ok := expectedContent(in.ToolName, in.ToolInput, cur); ok {
			if s.rb.expect[call] == nil {
				s.rb.expect[call] = map[string]string{}
			}
			s.rb.expect[call][rel] = hashText(text)
		}
	}
}

// trackAfter runs after a successful write tool call, with s.mu held. The
// result counts as the agent's only when it equals what the tool input
// implied; otherwise the file's ownership is unknown and it is not restored.
func (s *Session) trackAfter(call string, paths []string) {
	s.rb.init()
	expect := s.rb.expect[call]
	delete(s.rb.expect, call)
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return
	}
	defer root.Close()
	for _, p := range paths {
		rel := s.rollbackRel(p)
		if rel == "" {
			continue
		}
		if _, ok := s.rb.baseline[rel]; !ok {
			continue
		}
		cur, err := readState(root, rel)
		if err != nil {
			s.rb.untracked[rel] = err.Error()
			continue
		}
		s.rb.agentHash[rel] = cur.hash()
		if _, ok := s.rb.untracked[rel]; ok {
			continue
		}
		want, ok := expect[rel]
		switch {
		case !ok:
			s.rb.untracked[rel] = "도구 입력으로 결과를 재현할 수 없어 소유권을 확정할 수 없음"
		case want != cur.hash():
			s.rb.untracked[rel] = "도구 결과와 실제 파일이 달라 소유권을 확정할 수 없음(동시 편집 가능)"
		}
	}
}

// snapshotProgress keeps the agent-touched files as they are at a progress
// point a passing check confirmed (estimated progress from writes alone is
// not a safe point to return to). Called with s.mu held.
func (s *Session) snapshotProgress(seq int64, reason string) {
	s.rb.init()
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return
	}
	defer root.Close()
	for rel := range s.rb.agentHash {
		f, err := readState(root, rel)
		if err != nil {
			s.rb.untracked[rel] = err.Error()
			continue
		}
		s.keep(s.rb.snapshot, rel, f)
	}
	s.rb.snapshotSeq = seq
	s.goldenCapture(seq, reason)
}

type rollbackItem struct {
	Path    string `json:"path"`
	Action  string `json:"action"` // restore | delete
	Reason  string `json:"reason,omitempty"`
	current string
	target  fileState
}

// targetFn names the content a path returns to; ok is false with the reason
// when the plan has nothing to return it to.
type targetFn func(rel string) (target fileState, ok bool, reason string)

// rollbackPlan lists what a rollback to the last confirmed progress would do
// and its plan ID. Called with s.mu held.
func (s *Session) rollbackPlan() (todo, skipped []rollbackItem, id string) {
	s.rb.init()
	seen := map[string]bool{}
	var paths []string
	for rel := range s.rb.agentHash {
		seen[rel] = true
		paths = append(paths, rel)
	}
	for rel := range s.rb.untracked {
		if !seen[rel] {
			paths = append(paths, rel)
		}
	}
	target := func(rel string) (fileState, bool, string) {
		if t, ok := s.rb.snapshot[rel]; ok {
			return t, true, ""
		}
		if t, ok := s.rb.baseline[rel]; ok {
			return t, true, ""
		}
		return fileState{}, false, "되돌릴 기준 내용이 없음"
	}
	return s.planWith(paths, target, "rollback-plan", fmt.Sprint(s.rb.snapshotSeq))
}

// planWith builds a plan over paths: a file is restored only when it still
// holds what the agent last wrote, so a person's later edit is never
// overwritten. Called with s.mu held.
func (s *Session) planWith(paths []string, target targetFn, salt ...string) (todo, skipped []rollbackItem, id string) {
	sort.Strings(paths)
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		for _, rel := range paths {
			skipped = append(skipped, rollbackItem{Path: rel, Reason: "프로젝트 폴더를 열 수 없음"})
		}
		return nil, skipped, ""
	}
	defer root.Close()
	var parts []string
	for _, rel := range paths {
		if why, ok := s.rb.untracked[rel]; ok {
			skipped = append(skipped, rollbackItem{Path: rel, Reason: why})
			continue
		}
		target, ok, why := target(rel)
		if !ok {
			skipped = append(skipped, rollbackItem{Path: rel, Reason: why})
			continue
		}
		cur, err := readState(root, rel)
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
		todo = append(todo, rollbackItem{Path: rel, Action: action, current: cur.hash(), target: target})
		parts = append(parts, rel, action, cur.hash(), target.hash())
	}
	if len(todo) > 0 {
		id = fp.Hash(append(salt, parts...)...)[:8]
	}
	return todo, skipped, id
}

func (s *Session) rollbackBasis() string {
	if s.rb.snapshotSeq >= 0 {
		return fmt.Sprintf("마지막으로 진척이 확인된 시점(이벤트 %d)", s.rb.snapshotSeq)
	}
	return "AI가 이 세션에서 처음 쓰기 전 상태(아직 확인된 진척 없음)"
}

// rollback previews (arg "") or applies (arg "apply <plan ID>") a rollback to
// the last confirmed progress. "golden ..." addresses a recorded passing
// point instead. Applying needs the ID of a preview that is still valid, so
// what the user saw is exactly what is written. Called without s.mu.
func (s *Session) rollback(arg string) (string, error) {
	fields := strings.Fields(arg)
	if len(fields) > 0 && fields[0] == "golden" {
		return s.goldenRollback(fields[1:])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	todo, skipped, id := s.rollbackPlan()
	apply, applyID := false, ""
	if len(fields) > 0 && fields[0] == "apply" {
		apply = true
		if len(fields) > 1 {
			applyID = fields[1]
		}
	}
	return s.runRollback(todo, skipped, id, s.rollbackBasis(), "rollback", apply, applyID, s.rb.snapshotSeq)
}

// runRollback renders a plan and, when apply names its ID, writes it. Called
// with s.mu held.
func (s *Session) runRollback(todo, skipped []rollbackItem, id, basis, command string, apply bool, applyID string, basisSeq int64) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "되돌리기 기준: %s\n", basis)
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
		fmt.Fprintf(&b, "- %s: 대상 아님 (%s)\n", it.Path, it.Reason)
	}
	b.WriteString("셸 명령으로 바뀐 파일은 대상이 아니다.\n")
	if !apply || len(todo) == 0 {
		if len(todo) > 0 {
			fmt.Fprintf(&b, "미리보기다. 이대로 되돌리려면 /samcheonpo:%s apply %s 를 실행한다.\n", command, id)
		}
		return b.String(), nil
	}
	if applyID != id {
		b.WriteString("적용하지 않았다: 계획 ID가 없거나 미리본 뒤 파일이 바뀌었다. 위 내용을 확인하고 표시된 ID로 다시 실행한다.\n")
		fmt.Fprintf(&b, "현재 계획 ID: %s\n", id)
		return b.String(), nil
	}
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return "", err
	}
	defer root.Close()
	backup := filepath.Join(config.Home(), "backup", "rollback", s.ID, time.Now().UTC().Format("20060102T150405Z"))
	var done []rollbackItem
	var failed []string
	for _, it := range todo {
		cur, err := readState(root, it.Path)
		if err != nil || cur.hash() != it.current {
			failed = append(failed, it.Path+": 적용 직전에 내용이 바뀌어 건너뜀")
			continue
		}
		if !cur.absent {
			dst := filepath.Join(backup, filepath.FromSlash(it.Path))
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				failed = append(failed, it.Path+": 백업 실패: "+err.Error())
				continue
			}
			if err := os.WriteFile(dst, cur.data, 0o600); err != nil {
				failed = append(failed, it.Path+": 백업 실패: "+err.Error())
				continue
			}
		}
		if it.Action == "delete" {
			err = root.Remove(filepath.FromSlash(it.Path))
		} else {
			mode := it.target.mode
			if mode == 0 {
				mode = cur.mode
			}
			err = writeInRoot(root, it.Path, it.target.data, mode)
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
			s.ID, time.Now().UTC().Format(time.RFC3339Nano), basisSeq, string(restored), string(skip), strings.Join(failed, "\n"))
		s.recordStorageError(err)
	}
	fmt.Fprintf(&b, "%d개 파일을 되돌렸다.\n", len(done))
	if len(done) > 0 {
		fmt.Fprintf(&b, "바꾸기 전 내용은 %s 에 백업했다.\n", backup)
	}
	for _, f := range failed {
		fmt.Fprintf(&b, "실패: %s\n", f)
	}
	return b.String(), nil
}

// writeInRoot replaces a project file atomically without leaving the root.
func writeInRoot(root *os.Root, rel string, data []byte, mode os.FileMode) error {
	name := filepath.FromSlash(rel)
	if mode == 0 {
		mode = 0o644
	}
	if dir := filepath.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(name), ".samcheonpo-rollback-"+hex.EncodeToString(suffix[:]))
	if err := root.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}
