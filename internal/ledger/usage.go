package ledger

import (
	"os"
	"path/filepath"
	"time"
)

// Usage describes how much space a ledger takes and what fills it.
type Usage struct {
	Bytes        int64
	Sessions     int
	Events       int
	Verdicts     int
	Observations int
	Sealed       int
	Since        time.Time
}

// Usage reports the ledger's size and the rows that fill it. A ledger grows
// with every session and nothing in it ever shrinks, so a monitor that runs
// for months ends up with a file nobody can back up. The report is what
// makes that visible before it becomes a problem.
func (d *DB) Usage() (Usage, error) {
	var u Usage
	count := func(query string) (int, error) {
		var n int
		err := d.QueryRow(query).Scan(&n)
		return n, err
	}
	var err error
	if u.Sessions, err = count(`SELECT COUNT(*) FROM session`); err != nil {
		return u, err
	}
	if u.Events, err = count(`SELECT COUNT(*) FROM event`); err != nil {
		return u, err
	}
	if u.Verdicts, err = count(`SELECT COUNT(*) FROM verdict`); err != nil {
		return u, err
	}
	if u.Sealed, err = count(`SELECT COUNT(*) FROM session_seal`); err != nil {
		return u, err
	}
	if u.Observations, err = count(`SELECT COUNT(*) FROM event_observation`); err != nil {
		return u, err
	}
	return u, nil
}

// FileSize returns the ledger file's size on disk, including the write-ahead
// log that a running daemon keeps beside it.
func FileSize(path string) int64 {
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(path + suffix); err == nil {
			total += info.Size()
		}
	}
	return total
}

// Compact reclaims the free space a ledger holds after rows were deleted.
// It rewrites the file, so it needs exclusive access and it copies the
// ledger's size on disk while it runs. It never removes a row.
//
// The rewrite is the explicit answer to a ledger that does not vacuum
// itself. Automatic vacuum would put that work on the commit path, which
// is the path a hook waits for.
func (d *DB) Compact() error {
	_, err := d.Exec(`VACUUM`)
	return err
}

// PruneDays is the retention window the ledger reports.
//
// A session that was closed longer ago than this has its event-level detail
// removed when a retention pass is asked for. The seal, the receipt snapshot
// and the verdicts of that session stay, so the receipt still verifies
// against its seal afterwards.
const PruneDays = 90

// PruneReport says what a retention pass removed.
type PruneReport struct {
	Sessions     int
	Events       int
	Observations int
}

// Prune removes the bulk rows of sessions that were closed long ago.
//
// The seal, the receipt snapshot and the verdicts stay: they are the
// record, and a receipt must still verify against its seal after the
// cleanup. What goes is the event-level detail of old sessions, which is
// what fills the file.
//
// A session whose analysis was never stored is not pruned. Its events are
// the only copy of what was observed, and nothing would be left to show for
// them. A session that is still observed is not pruned either.
func (d *DB) Prune(olderThan time.Time) (PruneReport, error) {
	var r PruneReport
	// Only sessions whose observation was closed before the cutoff, that
	// are not being observed any more, and that have a stored analysis.
	const old = `SELECT o.session_id FROM session_observation o
		WHERE o.close_reason <> ? AND o.closed_at IS NOT NULL AND o.closed_at < ?
		AND EXISTS (SELECT 1 FROM receipt_snapshot rs WHERE rs.session_id = o.session_id)`
	if err := d.QueryRow(`SELECT COUNT(*) FROM (`+old+`)`, ObservationOpen, ts(olderThan)).Scan(&r.Sessions); err != nil {
		return r, err
	}
	// The observation rows carry only reason codes and fingerprints, so
	// they go first: they are the larger share and hold the least.
	res, err := d.Exec(`DELETE FROM event_observation WHERE session_id IN (`+old+`)`, ObservationOpen, ts(olderThan))
	if err != nil {
		return r, err
	}
	if n, err := res.RowsAffected(); err == nil {
		r.Observations = int(n)
	}
	res, err = d.Exec(`DELETE FROM event WHERE session_id IN (`+old+`)`, ObservationOpen, ts(olderThan))
	if err != nil {
		return r, err
	}
	if n, err := res.RowsAffected(); err == nil {
		r.Events = int(n)
	}
	return r, nil
}

// Path returns the ledger file's path.
func (d *DB) Path() string {
	var seq int
	var name, file string
	if err := d.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
		return ""
	}
	return filepath.Clean(file)
}
