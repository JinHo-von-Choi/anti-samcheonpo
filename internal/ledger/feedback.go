package ledger

import (
	"time"
)

// FeedbackStat is one rule's recorded user feedback counts.
type FeedbackStat struct {
	Rule          string
	FalsePositive int
	TruePositive  int
	Extend        int
}

// FalsePositive is the label the daemon records when it verifies that a
// verdict fired on ordinary behavior. Extend is the session-ceiling label.
const (
	LabelFalsePositive = "false_positive"
	LabelTruePositive  = "true_positive"
	LabelExtend        = "extend"
)

// FeedbackStats groups feedback rows by rule. A false-positive label is the
// user's own negative judgment of a verdict, so it is the only record in the
// ledger where the user, not the classifier, is the authority.
func (d *DB) FeedbackStats() ([]FeedbackStat, error) {
	rows, err := d.Query(`SELECT COALESCE(NULLIF(rule,''),'(규칙없음)'),
		SUM(CASE WHEN label=? THEN 1 ELSE 0 END),
		SUM(CASE WHEN label=? THEN 1 ELSE 0 END),
		SUM(CASE WHEN label=? THEN 1 ELSE 0 END)
		FROM feedback GROUP BY 1 ORDER BY 1`, LabelFalsePositive, LabelTruePositive, LabelExtend)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeedbackStat
	for rows.Next() {
		var f FeedbackStat
		if err := rows.Scan(&f.Rule, &f.FalsePositive, &f.TruePositive, &f.Extend); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FeedbackEvidenceRow is one feedback row reduced to what a provenance
// digest needs: rule identity, session, label, and time.
type FeedbackEvidenceRow struct {
	Rule    string
	Session string
	Label   string
	At      time.Time
}

// FeedbackEvidence returns feedback rows for provenance digests, oldest
// first. A rule provenance digest must reproduce from these rows alone;
// this method is that digest's only input besides blocks and attempts.
// A zero since means all history; SQLite cannot compare against NULL, so
// the floor is the earliest representable RFC3339 timestamp.
func (d *DB) FeedbackEvidence(since time.Time) ([]FeedbackEvidenceRow, error) {
	floor := ts(since)
	if since.IsZero() {
		floor = "0000-01-01T00:00:00Z"
	}
	rows, err := d.Query(`SELECT COALESCE(NULLIF(rule,''),'(규칙없음)'), COALESCE(session_id,''), label, created_at
		FROM feedback WHERE created_at >= ? ORDER BY created_at, rule, session_id`, floor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeedbackEvidenceRow
	for rows.Next() {
		var f FeedbackEvidenceRow
		var s string
		if err := rows.Scan(&f.Rule, &f.Session, &f.Label, &s); err != nil {
			return nil, err
		}
		if s != "" {
			f.At, _ = time.Parse(time.RFC3339, s)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
