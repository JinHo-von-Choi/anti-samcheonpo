package ledger

import (
	"database/sql"
	"encoding/json"
	"slices"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/correction"
)

// CorrectionRow is a stored correction rule.
type CorrectionRow struct {
	correction.Correction
	Project    string    `json:"project_path"`
	Evidence   []string  `json:"evidence"`
	Sessions   []string  `json:"sessions"`
	State      string    `json:"state"`
	Violations int       `json:"violations"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	ApprovedAt time.Time `json:"approved_at,omitzero"`
}

// NoteCorrection records that a user message in session gave c. A rule
// given with a lasting scope, or in two sessions, becomes a candidate; an
// approved or withdrawn rule keeps its state. It returns the stored row.
func (d *DB) NoteCorrection(project, session, msgHash string, c correction.Correction, now time.Time) (CorrectionRow, error) {
	id := correction.ID(project, c)
	row, err := d.Correction(id)
	if err == sql.ErrNoRows {
		row = CorrectionRow{Correction: c, Project: project, State: correction.StateObserved, CreatedAt: now}
		row.ID = id
	} else if err != nil {
		return row, err
	}
	if !slices.Contains(row.Evidence, msgHash) {
		row.Evidence = append(row.Evidence, msgHash)
	}
	if !slices.Contains(row.Sessions, session) {
		row.Sessions = append(row.Sessions, session)
	}
	row.Persistent = row.Persistent || c.Persistent
	if row.State == correction.StateObserved && (row.Persistent || len(row.Sessions) >= 2) {
		row.State = correction.StateCandidate
	}
	row.UpdatedAt = now
	return row, d.saveCorrection(row)
}

func (d *DB) saveCorrection(r CorrectionRow) error {
	ev, _ := json.Marshal(r.Evidence)
	ss, _ := json.Marshal(r.Sessions)
	var approved any
	if !r.ApprovedAt.IsZero() {
		approved = ts(r.ApprovedAt)
	}
	_, err := d.Exec(`INSERT INTO correction(id,project_path,kind,action,target,persistent,statement,evidence,sessions,state,violations,created_at,updated_at,approved_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET persistent=excluded.persistent,evidence=excluded.evidence,sessions=excluded.sessions,
		state=excluded.state,violations=excluded.violations,updated_at=excluded.updated_at,approved_at=excluded.approved_at`,
		r.ID, r.Project, r.Kind, r.Action, r.Target, b2i(r.Persistent), r.Statement, string(ev), string(ss), r.State, r.Violations, ts(r.CreatedAt), ts(r.UpdatedAt), approved)
	return err
}

const correctionColumns = `id,project_path,kind,action,target,persistent,statement,evidence,sessions,state,violations,created_at,updated_at,COALESCE(approved_at,'')`

func scanCorrection(sc interface{ Scan(...any) error }) (CorrectionRow, error) {
	var r CorrectionRow
	var persistent int
	var ev, ss, created, updated, approved string
	if err := sc.Scan(&r.ID, &r.Project, &r.Kind, &r.Action, &r.Target, &persistent, &r.Statement, &ev, &ss, &r.State, &r.Violations, &created, &updated, &approved); err != nil {
		return r, err
	}
	r.Persistent = persistent == 1
	_ = json.Unmarshal([]byte(ev), &r.Evidence)
	_ = json.Unmarshal([]byte(ss), &r.Sessions)
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	r.ApprovedAt, _ = time.Parse(time.RFC3339Nano, approved)
	return r, nil
}

// Correction returns one stored rule.
func (d *DB) Correction(id string) (CorrectionRow, error) {
	return scanCorrection(d.QueryRow(`SELECT `+correctionColumns+` FROM correction WHERE id=?`, id))
}

// Corrections returns the rules of a project ("" for every project) in the
// given states (all states when none is given).
func (d *DB) Corrections(project string, states ...string) ([]CorrectionRow, error) {
	rows, err := d.Query(`SELECT ` + correctionColumns + ` FROM correction ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CorrectionRow
	for rows.Next() {
		r, err := scanCorrection(rows)
		if err != nil {
			return nil, err
		}
		if project != "" && r.Project != project || len(states) > 0 && !slices.Contains(states, r.State) {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetCorrectionState approves (active) or withdraws a rule.
func (d *DB) SetCorrectionState(id, state string, now time.Time) error {
	r, err := d.Correction(id)
	if err != nil {
		return err
	}
	r.State, r.UpdatedAt = state, now
	if state == correction.StateActive {
		r.ApprovedAt = now
	}
	return d.saveCorrection(r)
}

// NoteCorrectionViolation counts one more break of a rule and returns the
// new count.
func (d *DB) NoteCorrectionViolation(id string) (int, error) {
	if _, err := d.Exec(`UPDATE correction SET violations=violations+1 WHERE id=?`, id); err != nil {
		return 0, err
	}
	var n int
	err := d.QueryRow(`SELECT violations FROM correction WHERE id=?`, id).Scan(&n)
	return n, err
}
