package seal

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
)

const TaskSpecVersion = "evidence-ledger/2"

// TaskSeal is separate from the immutable v1 event chain. It binds a v1 head
// to an ordered stream of redacted task records. Hashes detect modifications;
// they are not signatures and cannot prove who made the observations.
type TaskSeal struct {
	Spec       string `json:"spec"`
	TaskID     string `json:"task_id"`
	LegacyHead string `json:"legacy_head"`
	Rows       []Row  `json:"rows"`
	Head       string `json:"head"`
}

type taskRecord struct {
	Spec        string `json:"spec"`
	Kind        string `json:"kind"`
	TaskID      string `json:"task_id"`
	Revision    uint64 `json:"revision"`
	PayloadHash string `json:"payload_hash"`
}

func taskRow(kind, taskID string, revision uint64, payload any, previous string) (Row, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Row{}, err
	}
	d, err := json.Marshal(taskRecord{TaskSpecVersion, kind, taskID, revision, fp.Hash("task-record", string(b))})
	if err != nil {
		return Row{}, err
	}
	return Row{Kind: kind, Data: string(d), Chain: Link(previous, string(d))}, nil
}

func BuildTask(taskID, legacyHead string, revisions []intent.Revision, evidence []verification.Evidence) (TaskSeal, error) {
	s := TaskSeal{Spec: TaskSpecVersion, TaskID: taskID, LegacyHead: legacyHead}
	if taskID == "" || len(revisions) == 0 {
		return s, errors.New("task and revisions required")
	}
	// Bind the version, task and legacy chain even when there are no checks.
	header, _ := json.Marshal(struct{ Spec, TaskID, LegacyHead string }{s.Spec, taskID, legacyHead})
	previous := Link("", string(header))
	for i, r := range revisions {
		if err := r.Validate(); err != nil {
			return s, err
		}
		if r.TaskID != taskID || r.Number != uint64(i+1) {
			return s, errors.New("non-contiguous task revisions")
		}
		row, err := taskRow("intent", taskID, r.Number, r, previous)
		if err != nil {
			return s, err
		}
		s.Rows = append(s.Rows, row)
		previous = row.Chain
	}
	seen := map[string]bool{}
	for _, e := range evidence {
		if err := e.Validate(); err != nil {
			return s, err
		}
		if e.Key.TaskID != taskID || e.Key.Revision > uint64(len(revisions)) || seen[e.ID] {
			return s, errors.New("invalid or duplicate task evidence")
		}
		seen[e.ID] = true
		row, err := taskRow("verification", taskID, e.Key.Revision, e, previous)
		if err != nil {
			return s, err
		}
		s.Rows = append(s.Rows, row)
		previous = row.Chain
	}
	s.Head = previous
	return s, nil
}

func VerifyTask(s TaskSeal, revisions []intent.Revision, evidence []verification.Evidence) error {
	if s.Spec != TaskSpecVersion {
		return fmt.Errorf("unsupported task seal: %s", s.Spec)
	}
	want, err := BuildTask(s.TaskID, s.LegacyHead, revisions, evidence)
	if err != nil {
		return err
	}
	if s.Head != want.Head || len(s.Rows) != len(want.Rows) {
		return errors.New("task seal mismatch")
	}
	for i := range s.Rows {
		if s.Rows[i] != want.Rows[i] {
			return fmt.Errorf("task seal row %d mismatch", i)
		}
	}
	return nil
}
