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

// Observation close reasons. They say what stopped the observation. None of
// them says the agent ended, except ObservationSessionEnd, which is the
// agent's own report.
const (
	// ObservationOpen means the session is still being observed.
	ObservationOpen = "open"
	// ObservationSessionEnd means the agent reported that the session
	// ended.
	ObservationSessionEnd = "session_end"
	// ObservationDaemonShutdown means the daemon closed the observation
	// when it stopped.
	ObservationDaemonShutdown = "daemon_shutdown"
	// ObservationDaemonLost means the daemon that owned the observation
	// disappeared without closing it. The observation stops where the
	// daemon stopped.
	ObservationDaemonLost = "daemon_lost"
	// ObservationIdleTimeout means the session went silent for long
	// enough that the daemon released it.
	ObservationIdleTimeout = "idle_timeout"
	// ObservationAuditSnapshot means the analysis came from a source file
	// rather than from a live session.
	ObservationAuditSnapshot = "audit_snapshot"
)

func (d *DB) Observation(id string) (Observation, error) {
	var o Observation
	var last, closed string
	err := d.QueryRow(`SELECT agent,COALESCE(last_activity_at,''),COALESCE(closed_at,''),close_reason,evaluator_version FROM session_observation WHERE session_id=?`, id).Scan(&o.Agent, &last, &closed, &o.CloseReason, &o.Evaluator)
	o.LastActivity, _ = time.Parse(time.RFC3339Nano, last)
	o.ClosedAt, _ = time.Parse(time.RFC3339Nano, closed)
	return o, err
}

func (d *DB) startObservation(id, agent string) error {
	_, err := d.Exec(`INSERT INTO session_observation(session_id,agent,close_reason,evaluator_version) VALUES(?,?,?,?) ON CONFLICT(session_id) DO UPDATE SET closed_at=NULL,close_reason=?,evaluator_version=excluded.evaluator_version WHERE session_observation.agent=excluded.agent AND session_observation.close_reason<>?`,
		id, agent, ObservationOpen, analyze.EvaluatorVersion, ObservationOpen, ObservationOpen)
	return err
}
