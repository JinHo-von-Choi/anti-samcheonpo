package ledger

import (
	"database/sql"
	"sort"
	"time"
)

// DetectionTiming is where a session's first judgment fell in its
// trajectory.
//
// "Did the monitor catch it" is a yes-or-no question. "How early" is the
// question that decides whether the monitor is worth running at all: a
// judgment that arrives in the last third of a trajectory came after most
// of the cost was already spent, and the saving it produced is already
// gone.
//
// Position is the fraction of the trajectory that had elapsed when the
// first judgment was raised. Zero means the judgment came with the first
// event; one means it came with the last one.
type DetectionTiming struct {
	SessionID string `json:"session_id"`
	// Verdicts is how many judgments the session raised.
	Verdicts int `json:"verdicts"`
	// FirstVerdictSeq is the event sequence number the first judgment was
	// raised on.
	FirstVerdictSeq int64 `json:"first_verdict_seq"`
	// FirstEventSeq and LastEventSeq bound the observed trajectory.
	FirstEventSeq int64 `json:"first_event_seq"`
	LastEventSeq  int64 `json:"last_event_seq"`
	// Position is where the first judgment fell, from 0 to 1.
	Position float64 `json:"position"`
	// InLastThird marks a first judgment that arrived in the last third
	// of the trajectory.
	InLastThird bool `json:"in_last_third"`
	// FirstVerdictAt is when the first judgment was raised.
	FirstVerdictAt time.Time `json:"first_verdict_at,omitempty"`
	// LastActivityAt is when the last observed event happened.
	LastActivityAt time.Time `json:"last_activity_at,omitempty"`
	// LeadMS is the time from the first judgment to the end of the
	// observation, in milliseconds. It is the lead the monitor had, not a
	// grader's confirmation.
	LeadMS int64 `json:"lead_ms,omitempty"`
}

// DetectionTimingSummary aggregates first-judgment timing over sessions.
//
// The median position and the last-third share are the same two numbers
// the avoidance-timing literature reports, measured on what this monitor
// actually saw. They describe detection latency, not detection accuracy.
type DetectionTimingSummary struct {
	// Sessions is how many sessions were considered.
	Sessions int `json:"sessions"`
	// WithVerdict is how many of them raised at least one judgment.
	WithVerdict int `json:"with_verdict"`
	// WithoutVerdict is how many raised none.
	WithoutVerdict int `json:"without_verdict"`
	// MedianPosition is the median first-judgment position.
	MedianPosition float64 `json:"median_position"`
	// LastThirdCount is how many first judgments arrived in the last
	// third of their trajectory.
	LastThirdCount int `json:"last_third_count"`
	// LastThirdShare is LastThirdCount over WithVerdict.
	LastThirdShare float64 `json:"last_third_share"`
	// MedianLeadMS is the median lead in milliseconds.
	MedianLeadMS int64 `json:"median_lead_ms"`
	// EarliestPosition and LatestPosition bound the observed positions.
	EarliestPosition float64 `json:"earliest_position"`
	LatestPosition   float64 `json:"latest_position"`
}

// detectionTimingRow is one session's raw timing facts.
type detectionTimingRow struct {
	sessionID       string
	verdicts        int
	firstVerdictSeq int64
	firstEventSeq   int64
	lastEventSeq    int64
	firstVerdictAt  time.Time
	lastActivityAt  time.Time
}

// timingQuery selects the timing facts of the sessions in range.
//
// The subqueries are per session on purpose: a join over verdict and
// event would count every verdict once per event.
const timingQuery = `SELECT s.id,
 (SELECT COUNT(*) FROM verdict v WHERE v.session_id=s.id),
 (SELECT MIN(v.seq) FROM verdict v WHERE v.session_id=s.id),
 (SELECT MIN(e.seq) FROM event e WHERE e.session_id=s.id),
 (SELECT MAX(e.seq) FROM event e WHERE e.session_id=s.id),
 (SELECT e.ts FROM verdict v JOIN event e ON e.session_id=v.session_id AND e.seq=v.seq WHERE v.session_id=s.id ORDER BY v.seq LIMIT 1),
 (SELECT MAX(e.ts) FROM event e WHERE e.session_id=s.id)
 FROM session s WHERE (s.started_at IS NULL OR s.started_at >= ?)`

// DetectionTiming returns where one session's first judgment fell.
//
// A session with no judgment returns a zero timing and no error: the
// absence of a judgment is a fact to report, not a failure to read.
func (d *DB) DetectionTiming(sessionID string) (DetectionTiming, error) {
	row := d.QueryRow(timingQuery+` AND s.id=?`, "1970-01-01T00:00:00Z", sessionID)
	return scanDetectionTiming(row)
}

// scanDetectionTiming reads one timing row.
func scanDetectionTiming(row *sql.Row) (DetectionTiming, error) {
	var r detectionTimingRow
	var firstVerdictSeq, firstEventSeq, lastEventSeq sql.NullInt64
	var firstVerdictAt, lastActivityAt sql.NullString
	if err := row.Scan(&r.sessionID, &r.verdicts, &firstVerdictSeq, &firstEventSeq, &lastEventSeq, &firstVerdictAt, &lastActivityAt); err != nil {
		return DetectionTiming{}, err
	}
	t := DetectionTiming{SessionID: r.sessionID, Verdicts: r.verdicts}
	if firstEventSeq.Valid {
		t.FirstEventSeq = firstEventSeq.Int64
	}
	if lastEventSeq.Valid {
		t.LastEventSeq = lastEventSeq.Int64
	}
	if firstVerdictSeq.Valid {
		t.FirstVerdictSeq = firstVerdictSeq.Int64
	}
	if firstVerdictAt.Valid {
		if at, err := time.Parse(time.RFC3339Nano, firstVerdictAt.String); err == nil {
			t.FirstVerdictAt = at
		}
	}
	if lastActivityAt.Valid {
		if at, err := time.Parse(time.RFC3339Nano, lastActivityAt.String); err == nil {
			t.LastActivityAt = at
		}
	}
	applyDetectionPosition(&t)
	return t, nil
}

// applyDetectionPosition fills in the position and the lead.
//
// The denominator is the span between the first and the last observed
// event. A session with a single event has no span, so its position
// stays zero: the judgment came with everything there was, and there is
// nothing to be early about.
func applyDetectionPosition(t *DetectionTiming) {
	if t.Verdicts == 0 || t.FirstVerdictSeq == 0 {
		return
	}
	span := t.LastEventSeq - t.FirstEventSeq
	if span <= 0 {
		return
	}
	pos := float64(t.FirstVerdictSeq-t.FirstEventSeq) / float64(span)
	if pos < 0 {
		pos = 0
	}
	if pos > 1 {
		pos = 1
	}
	t.Position = pos
	t.InLastThird = pos >= 2.0/3.0
	if !t.FirstVerdictAt.IsZero() && !t.LastActivityAt.IsZero() && t.LastActivityAt.After(t.FirstVerdictAt) {
		t.LeadMS = t.LastActivityAt.Sub(t.FirstVerdictAt).Milliseconds()
	}
}

// DetectionTimings returns the timing of every session in range.
func (d *DB) DetectionTimings(since time.Time, agent string) ([]DetectionTiming, error) {
	q := timingQuery
	args := []any{since.UTC().Format(time.RFC3339Nano)}
	if agent != "" && agent != "all" {
		q += ` AND s.agent=?`
		args = append(args, agent)
	}
	q += ` ORDER BY s.id`
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DetectionTiming
	for rows.Next() {
		var r detectionTimingRow
		var firstVerdictSeq, firstEventSeq, lastEventSeq sql.NullInt64
		var firstVerdictAt, lastActivityAt sql.NullString
		if err := rows.Scan(&r.sessionID, &r.verdicts, &firstVerdictSeq, &firstEventSeq, &lastEventSeq, &firstVerdictAt, &lastActivityAt); err != nil {
			return nil, err
		}
		t := DetectionTiming{SessionID: r.sessionID, Verdicts: r.verdicts}
		if firstEventSeq.Valid {
			t.FirstEventSeq = firstEventSeq.Int64
		}
		if lastEventSeq.Valid {
			t.LastEventSeq = lastEventSeq.Int64
		}
		if firstVerdictSeq.Valid {
			t.FirstVerdictSeq = firstVerdictSeq.Int64
		}
		if firstVerdictAt.Valid {
			if at, err := time.Parse(time.RFC3339Nano, firstVerdictAt.String); err == nil {
				t.FirstVerdictAt = at
			}
		}
		if lastActivityAt.Valid {
			if at, err := time.Parse(time.RFC3339Nano, lastActivityAt.String); err == nil {
				t.LastActivityAt = at
			}
		}
		applyDetectionPosition(&t)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SummarizeDetectionTiming aggregates first-judgment timing.
//
// Only sessions that raised a judgment take part in the position
// statistics. A session with no judgment has no position, and counting
// it as a late judgment would invent lateness that was never observed.
func SummarizeDetectionTiming(timings []DetectionTiming) DetectionTimingSummary {
	s := DetectionTimingSummary{Sessions: len(timings)}
	var positions []float64
	var leads []int64
	for _, t := range timings {
		if t.Verdicts == 0 {
			s.WithoutVerdict++
			continue
		}
		s.WithVerdict++
		if t.Position <= 0 && t.LastEventSeq <= t.FirstEventSeq {
			// A single-event trajectory has no span to be early in.
			continue
		}
		positions = append(positions, t.Position)
		if t.LeadMS > 0 {
			leads = append(leads, t.LeadMS)
		}
		if t.InLastThird {
			s.LastThirdCount++
		}
	}
	if len(positions) > 0 {
		sort.Float64s(positions)
		s.MedianPosition = medianFloat(positions)
		s.EarliestPosition = positions[0]
		s.LatestPosition = positions[len(positions)-1]
		s.LastThirdShare = float64(s.LastThirdCount) / float64(len(positions))
	}
	if len(leads) > 0 {
		sort.Slice(leads, func(i, j int) bool { return leads[i] < leads[j] })
		s.MedianLeadMS = medianInt64(leads)
	}
	return s
}

// DetectionTimingSummary aggregates first-judgment timing over the
// sessions in range.
func (d *DB) DetectionTimingSummary(since time.Time, agent string) (DetectionTimingSummary, error) {
	timings, err := d.DetectionTimings(since, agent)
	if err != nil {
		return DetectionTimingSummary{}, err
	}
	return SummarizeDetectionTiming(timings), nil
}

// medianFloat returns the median of a sorted slice.
func medianFloat(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// medianInt64 returns the median of a sorted slice.
func medianInt64(sorted []int64) int64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
