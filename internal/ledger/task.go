package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
)

// SaveTaskRevision atomically creates a task and appends a revision. Exact
// replay is idempotent; reusing an identity with different content is an error.
func (d *DB) SaveTaskRevision(task intent.Task, r intent.Revision, links ...intent.SessionLink) error {
	if len(links) > 1 {
		return errors.New("at most one session link per revision")
	}
	if len(links) == 1 && (links[0].TaskID != task.ID || links[0].SessionID != r.Source.SessionID) {
		return errors.New("revision/session link mismatch")
	}
	if task.Version != intent.Version || task.ID == "" || task.ProjectID == "" || task.CreatedAt.IsZero() {
		return errors.New("invalid task")
	}
	if err := r.Validate(); err != nil {
		return err
	}
	if r.TaskID != task.ID {
		return errors.New("task/revision mismatch")
	}
	if r.Number > uint64(1<<63-1) {
		return errors.New("revision overflow")
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	commit := func() error {
		if len(links) == 1 {
			if err := saveTaskSession(tx, links[0]); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	if _, err = tx.Exec(`INSERT INTO task(id,version,project_id,created_at) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING`, task.ID, task.Version, task.ProjectID, ts(task.CreatedAt)); err != nil {
		return err
	}
	var version, project, created string
	if err = tx.QueryRow(`SELECT version,project_id,created_at FROM task WHERE id=?`, task.ID).Scan(&version, &project, &created); err != nil {
		return err
	}
	if version != task.Version || project != task.ProjectID || created != ts(task.CreatedAt) {
		return errors.New("task identity conflict")
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}
	var old string
	err = tx.QueryRow(`SELECT payload FROM intent_revision WHERE task_id=? AND revision=?`, r.TaskID, r.Number).Scan(&old)
	if err == nil {
		if old != string(payload) {
			return errors.New("revision identity conflict")
		}
		return commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var previous uint64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(revision),0) FROM intent_revision WHERE task_id=?`, r.TaskID).Scan(&previous); err != nil {
		return err
	}
	if previous != r.Parent {
		return errors.New("revision parent is not current")
	}
	if _, err = tx.Exec(`INSERT INTO intent_revision(task_id,revision,payload) VALUES(?,?,?)`, r.TaskID, r.Number, string(payload)); err != nil {
		return err
	}
	return commit()
}

func (d *DB) LatestIntent(taskID string) (intent.Revision, error) {
	var payload string
	var r intent.Revision
	if err := d.QueryRow(`SELECT payload FROM intent_revision WHERE task_id=? ORDER BY revision DESC LIMIT 1`, taskID).Scan(&payload); err != nil {
		return r, err
	}
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		return r, err
	}
	return r, r.Validate()
}

func (d *DB) LinkTaskSession(link intent.SessionLink) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveTaskSession(tx, link); err != nil {
		return err
	}
	return tx.Commit()
}

func saveTaskSession(tx *sql.Tx, link intent.SessionLink) error {
	if link.TaskID == "" || link.Agent == "" || link.SessionID == "" {
		return errors.New("incomplete session link")
	}
	if (link.ParentAgent == "") != (link.ParentSessionID == "") || (link.Agent == link.ParentAgent && link.SessionID == link.ParentSessionID) {
		return errors.New("invalid session parent")
	}
	b, err := json.Marshal(link)
	if err != nil {
		return err
	}
	if link.ParentSessionID != "" {
		var task string
		if err = tx.QueryRow(`SELECT task_id FROM task_session WHERE agent=? AND session_id=?`, link.ParentAgent, link.ParentSessionID).Scan(&task); err != nil {
			return fmt.Errorf("unresolved parent: %w", err)
		}
		if task != link.TaskID {
			return errors.New("parent belongs to another task")
		}
	}
	_, err = tx.Exec(`INSERT INTO task_session(agent,session_id,task_id,payload) VALUES(?,?,?,?) ON CONFLICT(agent,session_id) DO NOTHING`, link.Agent, link.SessionID, link.TaskID, string(b))
	if err != nil {
		return err
	}
	var old string
	if err = tx.QueryRow(`SELECT payload FROM task_session WHERE agent=? AND session_id=?`, link.Agent, link.SessionID).Scan(&old); err != nil {
		return err
	}
	if old != string(b) {
		return errors.New("session link identity conflict")
	}
	return nil
}

// SessionIntent restores the last committed revision and its immutable link
// in one read. A missing session is sql.ErrNoRows, not an invented empty task.
func (d *DB) SessionIntent(agent, session string) (intent.Task, intent.Revision, intent.SessionLink, error) {
	var task intent.Task
	var revision intent.Revision
	var link intent.SessionLink
	var created, payload, linked string
	err := d.QueryRow(`SELECT t.id,t.version,t.project_id,t.created_at,r.payload,s.payload
	FROM task_session s JOIN task t ON t.id=s.task_id JOIN intent_revision r ON r.task_id=t.id
	WHERE s.agent=? AND s.session_id=? ORDER BY r.revision DESC LIMIT 1`, agent, session).Scan(&task.ID, &task.Version, &task.ProjectID, &created, &payload, &linked)
	if err != nil {
		return task, revision, link, err
	}
	if task.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return task, revision, link, err
	}
	if err = json.Unmarshal([]byte(payload), &revision); err != nil {
		return task, revision, link, err
	}
	if err = json.Unmarshal([]byte(linked), &link); err != nil {
		return task, revision, link, err
	}
	if task.Version != intent.Version || task.ProjectID == "" || revision.TaskID != task.ID || link.TaskID != task.ID || link.Agent != agent || link.SessionID != session {
		return task, revision, link, errors.New("inconsistent task/session identity")
	}
	return task, revision, link, revision.Validate()
}

func (d *DB) SaveEvidence(e verification.Evidence) error {
	if err := e.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO verification_evidence(id,task_id,revision,key_digest,payload) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, e.ID, e.Key.TaskID, e.Key.Revision, e.Key.Digest(), string(b))
	if err != nil {
		return err
	}
	var old string
	if err = tx.QueryRow(`SELECT payload FROM verification_evidence WHERE id=?`, e.ID).Scan(&old); err != nil {
		return err
	}
	if old != string(b) {
		return errors.New("evidence identity conflict")
	}
	return tx.Commit()
}

func (d *DB) Evidence(key verification.Key) ([]verification.Evidence, error) {
	if !key.Valid() {
		return nil, errors.New("incomplete evidence key")
	}
	rows, err := d.Query(`SELECT payload FROM verification_evidence WHERE key_digest=? ORDER BY rowid`, key.Digest())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []verification.Evidence
	for rows.Next() {
		var payload string
		var e verification.Evidence
		if err = rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(payload), &e); err != nil {
			return nil, err
		}
		if err = e.Validate(); err != nil {
			return nil, err
		}
		if e.Key != key {
			return nil, errors.New("evidence key mismatch")
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
