package ledger

import (
	"time"
)

// BlockCount is one rule's count of interventions that stopped a tool call
// before it ran.
type BlockCount struct {
	Rule  string
	Agent string
	Count int
}

// BlocksSince counts the interventions that stopped a tool call before it
// ran, per rule and agent.
//
// A block is not a recovery prescription, so it never appears among the
// prescription stages the interventions report counts. It lives in the
// intervention table on the agent channel, and the blocked tool call is
// recorded with exit code -1. Counting it here keeps "the monitor stopped
// the call" visible next to "the monitor advised the call".
//
// The rule comes from the intervention row itself. Rows written before the
// rule column existed fall back to the verdict table, and a row whose
// verdict was rewritten away is reported under an empty rule rather than
// dropped.
func (d *DB) BlocksSince(since time.Time) ([]BlockCount, error) {
	rows, err := d.Query(`SELECT COALESCE(NULLIF(i.rule,''), COALESCE(v.rule,'')), COALESCE(s.agent,''), COUNT(*)
		FROM intervention i
		LEFT JOIN verdict v ON v.id = i.verdict_id
		LEFT JOIN session s ON s.id = i.session_id
		WHERE i.blocked = 1 AND s.started_at >= ?
		GROUP BY 1, 2`, ts(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlockCount
	for rows.Next() {
		var c BlockCount
		if err := rows.Scan(&c.Rule, &c.Agent, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// BlockEvidenceRow is one (rule, agent, session) group of stopped calls,
// reduced to what a provenance digest needs.
type BlockEvidenceRow struct {
	Rule    string
	Agent   string
	Session string
	Count   int
}

// BlockEvidence returns blocked-call groups over all history for the rule
// provenance digests. Unlike BlocksSince it needs no session start bound,
// so a zero-floor constant stands in for "all time": SQLite cannot compare
// a timestamp against NULL, and ts() of a zero time is NULL.
func (d *DB) BlockEvidence() ([]BlockEvidenceRow, error) {
	rows, err := d.Query(`SELECT COALESCE(NULLIF(i.rule,''), COALESCE(v.rule,'')), COALESCE(s.agent,''), i.session_id, COUNT(*)
		FROM intervention i
		LEFT JOIN verdict v ON v.id = i.verdict_id
		LEFT JOIN session s ON s.id = i.session_id
		WHERE i.blocked = 1
		GROUP BY 1, 2, 3
		ORDER BY 1, 2, 3`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlockEvidenceRow
	for rows.Next() {
		var b BlockEvidenceRow
		if err := rows.Scan(&b.Rule, &b.Agent, &b.Session, &b.Count); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BlockedRulesForSession counts the interventions that stopped a tool call
// before it ran, per rule, for one session.
func (d *DB) BlockedRulesForSession(id string) (map[string]int, error) {
	rows, err := d.Query(`SELECT COALESCE(NULLIF(i.rule,''), COALESCE(v.rule,'')), COUNT(*)
		FROM intervention i
		LEFT JOIN verdict v ON v.id = i.verdict_id
		WHERE i.session_id = ? AND i.blocked = 1
		GROUP BY 1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var rule string
		var n int
		if err := rows.Scan(&rule, &n); err != nil {
			return nil, err
		}
		out[rule] += n
	}
	return out, rows.Err()
}
