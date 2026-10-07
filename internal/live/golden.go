package live

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/patch"
)

// goldenManager returns the project's record of passing points. Called with
// s.mu held.
func (s *Session) goldenManager() *patch.GoldenStateManager {
	if s.golden == nil {
		s.golden = patch.NewGoldenStateManager(s.Root)
	}
	return s.golden
}

// goldenCapture records the agent's files as they are at a confirmed pass,
// durably under .samcheonpo/snapshots, so a later session can still return to
// a point that was verified. Only files whose ownership is confirmed are
// recorded, which keeps a restore from ever touching a person's edit. The
// write happens off the hook path; the bytes were read under s.mu and are not
// mutated afterwards. Called with s.mu held.
func (s *Session) goldenCapture(seq int64, reason string) {
	files := make(map[string]patch.File, len(s.rb.snapshot))
	for rel, f := range s.rb.snapshot {
		if _, untracked := s.rb.untracked[rel]; untracked || f.absent {
			continue
		}
		if _, owned := s.rb.agentHash[rel]; !owned {
			continue
		}
		files[rel] = patch.File{Data: f.data, Mode: f.mode}
	}
	if len(files) == 0 {
		return
	}
	meta := patch.SnapshotMeta{Reason: reason, Session: s.ID, Seq: seq}
	if s.ws != nil {
		if tree, fresh := s.ws.Current(); fresh {
			meta.Tree = tree
		}
	}
	g := s.goldenManager()
	go func() {
		if _, err := g.Capture(meta, files); err != nil {
			fmt.Fprintf(os.Stderr, "samcheonpo: 통과 시점 기록 실패: %v\n", err)
		}
	}()
}

// goldenRollback lists recorded passing points (no argument), previews a
// return to one ("<id>") or applies a previewed plan ("<id> apply <plan>").
// Only files this session's agent wrote, and that still hold what it wrote,
// are returned; everything else is listed as not covered. Called without
// s.mu.
func (s *Session) goldenRollback(fields []string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.goldenManager()
	if len(fields) == 0 {
		list := g.ListSnapshots()
		if len(list) == 0 {
			return "기록된 통과 시점이 없다. 완료 조건이나 검사가 통과하면 그 시점의 AI 파일을 기록한다.\n", nil
		}
		var b strings.Builder
		b.WriteString("기록된 통과 시점 (최신 먼저):\n")
		for _, m := range list {
			fmt.Fprintf(&b, "- %s  %s  %s  (파일 %d개, 이벤트 %d)\n", m.ID, m.Timestamp.Local().Format(time.DateTime), m.Reason, len(m.Files), m.Seq)
		}
		b.WriteString("미리보기: /samcheonpo:rollback golden <id>\n")
		return b.String(), nil
	}
	id := fields[0]
	meta, err := g.Get(id)
	if err != nil {
		if errors.Is(err, patch.ErrSnapshotGone) {
			return "그 통과 시점은 없거나 이미 정리됐다. /samcheonpo:rollback golden 으로 목록을 확인한다.\n", nil
		}
		return "", err
	}
	s.rb.init()
	var paths []string
	var skipped []rollbackItem
	for _, rel := range meta.Files {
		if _, owned := s.rb.agentHash[rel]; owned {
			paths = append(paths, rel)
			continue
		}
		if _, why := s.rb.untracked[rel]; why {
			paths = append(paths, rel)
			continue
		}
		skipped = append(skipped, rollbackItem{Path: rel, Reason: "이 세션의 AI가 쓴 파일이 아니라 소유권을 확정할 수 없음"})
	}
	target := func(rel string) (fileState, bool, string) {
		f, err := g.ReadFile(id, rel)
		if err != nil {
			return fileState{}, false, err.Error()
		}
		return fileState{data: f.Data, mode: f.Mode}, true, ""
	}
	todo, more, planID := s.planWith(paths, target, "golden-plan", id)
	skipped = append(skipped, more...)
	apply, applyID := false, ""
	if len(fields) > 1 && fields[1] == "apply" {
		apply = true
		if len(fields) > 2 {
			applyID = fields[2]
		}
	}
	basis := fmt.Sprintf("기록된 통과 시점 %s (%s, 이벤트 %d)", id, meta.Reason, meta.Seq)
	return s.runRollback(todo, skipped, planID, basis, "rollback golden "+id, apply, applyID, meta.Seq)
}
