package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
)

func (d *DB) SaveRecovery(a recovery.Attempt) error {
	if err := a.Validate(); err != nil {
		return err
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRow(`SELECT payload FROM recovery_attempt WHERE id=?`, a.ID).Scan(&raw)
	if err == nil {
		var old recovery.Attempt
		if err = json.Unmarshal([]byte(raw), &old); err != nil {
			return err
		}
		if old.Agent != a.Agent || old.SessionID != a.SessionID || old.Revision != a.Revision || old.CauseKey != a.CauseKey || old.VerdictID != a.VerdictID || old.Seq != a.Seq || old.Rule != a.Rule || old.Prescription != a.Prescription || old.Route != a.Route || !old.CreatedAt.Equal(a.CreatedAt) {
			return errors.New("recovery identity conflict")
		}
		for _, pair := range [][2]*time.Time{{old.EmittedAt, a.EmittedAt}, {old.DeliveredAt, a.DeliveredAt}, {old.AcknowledgedAt, a.AcknowledgedAt}} {
			if pair[0] != nil && (pair[1] == nil || !pair[0].Equal(*pair[1])) {
				return errors.New("recovery timestamp cannot be replaced")
			}
		}
		if old.DeliveredAt != nil && old.DeliveredSeq != a.DeliveredSeq {
			return errors.New("delivery observation boundary cannot be replaced")
		}
		if a.ObservedThrough < old.ObservedThrough {
			return errors.New("observation boundary moved backwards")
		}
		old.Observation = a.Observation
		if err = old.Advance(a.Stage, time.Now()); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	} else if a.Stage != recovery.Proposed {
		return errors.New("recovery must start as a proposal")
	}
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO recovery_attempt(id,agent,session_id,revision,payload) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, a.ID, a.Agent, a.SessionID, a.Revision, string(b))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) Recoveries(agent, session string) ([]recovery.Attempt, error) {
	rows, err := d.Query(`SELECT payload FROM recovery_attempt WHERE agent=? AND session_id=? ORDER BY rowid`, agent, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recovery.Attempt
	for rows.Next() {
		var raw string
		var a recovery.Attempt
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &a); err != nil {
			return nil, err
		}
		if err = a.Validate(); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RecoveryAttemptsSince returns every recorded recovery attempt created at or
// after since, across sessions, oldest first.
func (d *DB) RecoveryAttemptsSince(since time.Time) ([]recovery.Attempt, error) {
	rows, err := d.Query(`SELECT payload FROM recovery_attempt ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recovery.Attempt
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var a recovery.Attempt
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			return nil, err
		}
		if !a.CreatedAt.Before(since) {
			out = append(out, a)
		}
	}
	return out, rows.Err()
}
