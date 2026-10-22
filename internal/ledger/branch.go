package ledger

import (
	"fmt"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// BranchStateDiverged marks a session that was forked away from.
const BranchStateDiverged = "branch_diverged"

// BranchInfo describes a fork.
type BranchInfo struct {
	SourceID string
	NewID    string
	Agent    string
	Root     string
	Snapshot string
	Files    int
}

// LatestLiveSession returns the most recent live session of the project.
// A project with no live session is an error, not an empty result.
func (d *DB) LatestLiveSession() (id, agent, root string, err error) {
	row := d.QueryRow(`SELECT id, agent, project_path FROM session WHERE mode='live' ORDER BY started_at DESC LIMIT 1`)
	if err := row.Scan(&id, &agent, &root); err != nil {
		return "", "", "", fmt.Errorf("이 프로젝트에 진행 중인 세션이 없다: %w", err)
	}
	return id, agent, root, nil
}

// SessionRoot returns the agent and project path of a session.
func (d *DB) SessionRoot(id string) (agent, root string, err error) {
	row := d.QueryRow(`SELECT agent, project_path FROM session WHERE id=?`, id)
	if err := row.Scan(&agent, &root); err != nil {
		return "", "", fmt.Errorf("세션을 읽을 수 없음(%s): %w", id, err)
	}
	return agent, root, nil
}

// SessionBranchState returns the branch state of a session.
func (d *DB) SessionBranchState(id string) (string, error) {
	var state string
	if err := d.QueryRow(`SELECT branch_state FROM session WHERE id=?`, id).Scan(&state); err != nil {
		return "", err
	}
	return state, nil
}

// BranchSession starts a new session from a recorded passing point.
//
// The failed session stays as it is, marked as diverged, so a receipt can
// tell a branch from a session that simply ended. The new session starts
// with the recovery note as its first event, so the agent's own transcript
// says where it is and what to do next.
func (d *DB) BranchSession(oldID, newID, note string) (BranchInfo, error) {
	var agent, root, model, transcript string
	err := d.QueryRow(`SELECT agent, project_path, model, source_path FROM session WHERE id=?`, oldID).
		Scan(&agent, &root, &model, &transcript)
	if err != nil {
		return BranchInfo{}, fmt.Errorf("기준 세션을 읽을 수 없음(%s): %w", oldID, err)
	}
	if newID == "" {
		return BranchInfo{}, fmt.Errorf("새 세션 이름이 없다")
	}
	if err := d.UpsertLive(newID, agent, root, transcript, model); err != nil {
		return BranchInfo{}, err
	}
	if _, err := d.Exec(`UPDATE session SET branch_state=? WHERE id=?`, BranchStateDiverged, oldID); err != nil {
		return BranchInfo{}, err
	}
	ev := &event.Event{SessionID: newID, Seq: 1, TS: time.Now().UTC(), Kind: event.KindPrompt, Summary: note}
	if err := d.InsertLiveEvent(newID, ev); err != nil {
		return BranchInfo{}, err
	}
	return BranchInfo{SourceID: oldID, NewID: newID, Agent: agent, Root: root}, nil
}
