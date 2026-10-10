package ledger

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"time"
)

// Observation describes harness activity, not proof of task completion.
// Its absence means legacy/unknown, never an inferred SessionEnd.
type Observation struct {
	Agent, CloseReason, Evaluator string
	LastActivity, ClosedAt        time.Time
}

func (d *DB) Observation(id string) (Observation, error) {
	var o Observation
	var last, closed string
	err := d.QueryRow(`SELECT agent,COALESCE(last_activity_at,''),COALESCE(closed_at,''),close_reason,evaluator_version FROM session_observation WHERE session_id=?`, id).Scan(&o.Agent, &last, &closed, &o.CloseReason, &o.Evaluator)
	o.LastActivity, _ = time.Parse(time.RFC3339Nano, last)
	o.ClosedAt, _ = time.Parse(time.RFC3339Nano, closed)
	return o, err
}

func (d *DB) startObservation(id, agent string) error {
	_, err := d.Exec(`INSERT INTO session_observation(session_id,agent,close_reason,evaluator_version) VALUES(?,?,'open',?) ON CONFLICT(session_id) DO UPDATE SET closed_at=NULL,close_reason='open',evaluator_version=excluded.evaluator_version WHERE session_observation.agent=excluded.agent AND session_observation.close_reason<>'open'`, id, agent, analyze.EvaluatorVersion)
	return err
}
