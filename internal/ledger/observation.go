package ledger

import (
	"database/sql"
	"errors"
)

// ObservationGap is sticky: a later restart must not turn missing evidence
// into a complete observation stream.
func (d *DB) ObservationGap(agent, session string) (uint64, error) {
	var n uint64
	err := d.QueryRow(`SELECT rejected FROM observation_gap WHERE agent=? AND session_id=?`, agent, session).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

func (d *DB) SaveObservationGap(agent, session string, n uint64) error {
	if agent == "" || session == "" || n == 0 {
		return errors.New("observation gap requires identity and positive count")
	}
	_, err := d.Exec(`INSERT INTO observation_gap(agent,session_id,rejected) VALUES(?,?,?) ON CONFLICT(agent,session_id) DO UPDATE SET rejected=MAX(observation_gap.rejected,excluded.rejected)`, agent, session, n)
	return err
}
