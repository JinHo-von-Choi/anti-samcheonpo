package ledger

import (
	"encoding/json"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/seal"
)

// SaveSnapshot stores an analysis result and its seal without rewriting the
// live rows.
//
// The live writer owns the event and verdict tables and keeps them current
// one event at a time. A snapshot taken between turns does not replace those
// rows; it only records what the analysis found, so a receipt can be issued
// later without running the analysis again. The seal travels with the
// snapshot, so the stored result is verified against it.
//
// The snapshot does not close the observation. A session that is still being
// observed stays open, and an open session does not get a receipt.
func (d *DB) SaveSnapshot(r *analyze.Result) (seal.Seal, error) {
	sl, _ := BuildSeal(r)
	payload, err := encodeSnapshot(r)
	if err != nil {
		return sl, err
	}
	tx, err := d.Begin()
	if err != nil {
		return sl, err
	}
	defer tx.Rollback()
	sj, _ := json.Marshal(sl)
	if _, err := tx.Exec(`INSERT OR REPLACE INTO session_seal(session_id, seal) VALUES (?,?)`, r.Session.ID, string(sj)); err != nil {
		return sl, err
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO receipt_snapshot(session_id,payload) VALUES (?,?)`, r.Session.ID, string(payload)); err != nil {
		return sl, err
	}
	return sl, tx.Commit()
}

// CloseLostObservations closes the observations that no daemon owns any more.
//
// Only one daemon runs at a time, so an observation that is still open when
// a daemon starts belongs to a daemon that is gone. Leaving it open would
// put its session out of reach: an open observation is refused a receipt,
// because "still being observed" and "nobody is watching it any more" must
// not read the same.
//
// The reason says what happened. It never says the agent ended.
func (d *DB) CloseLostObservations(reason string, at time.Time) (int, error) {
	res, err := d.Exec(`UPDATE session_observation SET closed_at=?, close_reason=? WHERE close_reason='open' AND closed_at IS NULL`,
		at.UTC().Format(time.RFC3339Nano), reason)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
