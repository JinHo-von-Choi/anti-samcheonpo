package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckpointPersistsTaskEvidenceAndFollowupInvalidates(t *testing.T) {
	s, d := reliabilitySession(t)
	if err := os.MkdirAll(contract.Dir(s.Root), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "input"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	raw := `spec: progress-contract/2
goal: task-bound evidence
done:
  - id: check
    check: test -s input
    pure: true
    reuse:
      inputs: [input]
      environment_files: [input]
      deterministic: true
      max_age_sec: 60
`
	if err := os.WriteFile(contract.Path(s.Root), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.command(CommandInput{Name: "accept", Session: s.ID}); err != nil {
		t.Fatal(err)
	}
	results := s.checkpoint(false)
	if len(results) != 1 || results[0].Evidence == nil || !results[0].Pass {
		t.Fatalf("%+v", results)
	}
	e := results[0].Evidence
	if e.Key.TaskID != s.task.ID || e.Key.Revision != s.intentRevision.Number || e.SessionID != s.ID {
		t.Fatalf("wrong attribution: %+v", e)
	}
	evidence, err := s.db.TaskEvidence(s.task.ID, s.intentRevision.Number)
	if err != nil || len(evidence) != 1 || evidence[0].ID != e.ID {
		t.Fatalf("%+v %v", evidence, err)
	}
	s.onPrompt(HookInput{Prompt: "추가 설명입니다"})
	s.mu.Lock()
	met, _ := s.criteria()
	s.mu.Unlock()
	if met != 0 {
		t.Fatal("previous revision retained green completion")
	}
	if results = s.checkpoint(false); len(results) != 1 || results[0].Reused || results[0].Evidence.Key.Revision == e.Key.Revision {
		t.Fatalf("old revision reused: %+v", results)
	}
}
