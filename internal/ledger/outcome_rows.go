package ledger

import (
	"encoding/json"
	"time"
)

// InterventionRow is one proposed intervention with the verdict evidence
// the outcome measure starts from.
type InterventionRow struct {
	VerdictID string
	Session   string
	Agent     string
	Seq       int64
	Rule      string
	Arm       string
	Channel   string
	Evidence  []int64
}

// InterventionsSince lists the interventions of sessions started since t,
// in every channel including the experiment's control arm.
func (d *DB) InterventionsSince(since time.Time) ([]InterventionRow, error) {
	rows, err := d.Query(`SELECT i.verdict_id, i.session_id, COALESCE(s.agent,''), i.seq, COALESCE(NULLIF(i.rule,''), COALESCE(v.rule,'')),
		COALESCE(i.arm,''), COALESCE(i.channel,''), COALESCE(v.evidence_event_ids,'[]')
		FROM intervention i
		LEFT JOIN verdict v ON v.id = i.verdict_id AND v.session_id = i.session_id
		LEFT JOIN session s ON s.id = i.session_id
		WHERE s.started_at >= ?
		ORDER BY i.session_id, i.seq`, ts(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InterventionRow
	for rows.Next() {
		var r InterventionRow
		var evidence string
		if err := rows.Scan(&r.VerdictID, &r.Session, &r.Agent, &r.Seq, &r.Rule, &r.Arm, &r.Channel, &evidence); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(evidence), &r.Evidence)
		out = append(out, r)
	}
	return out, rows.Err()
}
