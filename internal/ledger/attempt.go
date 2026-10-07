package ledger

import "time"

// FailedAttempt is one edit that was followed by a failing verification in a
// task revision. It outlives sessions so a later session, a compacted one or
// another agent working on the same task can see what was already tried.
type FailedAttempt struct {
	TaskID    string
	Revision  uint64
	EditFP    string
	Path      string
	FailureFP string
	SessionID string
	Agent     string
	Seq       int64
}

// SaveFailedAttempts records failed edits; a repeat of the same edit with the
// same failure in the same revision is stored once.
func (d *DB) SaveFailedAttempts(as []FailedAttempt) error {
	if len(as) == 0 {
		return nil
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, a := range as {
		if _, err := tx.Exec(`INSERT INTO failed_attempt(task_id,revision,edit_fp,path,failure_fp,session_id,agent,seq,created_at) VALUES(?,?,?,?,?,?,?,?,?)
			ON CONFLICT(task_id,revision,edit_fp,failure_fp) DO NOTHING`, a.TaskID, a.Revision, a.EditFP, a.Path, a.FailureFP, a.SessionID, a.Agent, a.Seq, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FailedAttempts returns the failed edits of one task revision.
func (d *DB) FailedAttempts(taskID string, revision uint64) ([]FailedAttempt, error) {
	rows, err := d.Query(`SELECT task_id,revision,edit_fp,path,failure_fp,session_id,agent,seq FROM failed_attempt WHERE task_id=? AND revision=? ORDER BY created_at`, taskID, revision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FailedAttempt
	for rows.Next() {
		var a FailedAttempt
		if err := rows.Scan(&a.TaskID, &a.Revision, &a.EditFP, &a.Path, &a.FailureFP, &a.SessionID, &a.Agent, &a.Seq); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
