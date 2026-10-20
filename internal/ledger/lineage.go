package ledger

import (
	"encoding/json"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
)

// FirstVerdict reports when a session's first judgment was raised.
//
// The verdict table keeps the event sequence rather than the time, so the
// time comes from the event the judgment was raised on. The first judgment
// is what the monitor said first; how long before the session ended it
// said it is the lead the monitor had.
func (d *DB) FirstVerdict(sessionID string) (time.Time, string, error) {
	var seq int64
	var rule string
	if err := d.QueryRow(`SELECT seq, rule FROM verdict WHERE session_id=? ORDER BY seq LIMIT 1`, sessionID).Scan(&seq, &rule); err != nil {
		return time.Time{}, "", err
	}
	var ts string
	if err := d.QueryRow(`SELECT ts FROM event WHERE session_id=? AND seq=?`, sessionID, seq).Scan(&ts); err != nil {
		return time.Time{}, "", err
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, "", err
	}
	return at, rule, nil
}

// LastActivity reports when a session's last observed event happened.
func (d *DB) LastActivity(sessionID string) (time.Time, error) {
	var ts string
	if err := d.QueryRow(`SELECT COALESCE(MAX(ts),'') FROM event WHERE session_id=?`, sessionID).Scan(&ts); err != nil {
		return time.Time{}, err
	}
	if ts == "" {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, err
	}
	return at, nil
}

// LineageRevision is one goal revision of a task.
type LineageRevision struct {
	Revision uint64    `json:"revision"`
	Goal     string    `json:"goal"`
	At       time.Time `json:"at"`
}

// LineageVerdict is one judgment inside a session's chain.
type LineageVerdict struct {
	Rule     string    `json:"rule"`
	Detector string    `json:"detector"`
	Level    int       `json:"level"`
	Seq      int64     `json:"seq"`
	At       time.Time `json:"at,omitempty"`
	// Blocked marks a judgment that was actually enforced rather than
	// only announced.
	Blocked bool `json:"blocked,omitempty"`
}

// LineageSession is one session that worked on a task.
type LineageSession struct {
	Agent   string `json:"agent"`
	Session string `json:"session_id"`
	// ParentAgent and ParentSession say which session handed this one
	// its work. They are what makes the chain a chain.
	ParentAgent   string `json:"parent_agent,omitempty"`
	ParentSession string `json:"parent_session_id,omitempty"`
	// Verdicts counts the judgments the session raised.
	Verdicts int `json:"verdicts"`
	// Judgments lists them in the order they were raised. The count
	// alone says that something happened; the list says what.
	Judgments []LineageVerdict `json:"judgments,omitempty"`
	// FirstVerdict is when the session's first judgment was raised.
	FirstVerdict time.Time `json:"first_verdict,omitempty"`
	// LastActivity is when the session's last observed event happened.
	LastActivity time.Time `json:"last_activity,omitempty"`
	// LeadMS is how many milliseconds before the session ended the first
	// judgment came. It is the lead the monitor had, measured to the end
	// of the observation. It is not a grader's confirmation.
	LeadMS int64 `json:"lead_ms,omitempty"`
	// Position is where the first judgment fell in the trajectory, from
	// 0 to 1. A judgment in the last third came after most of the cost
	// was already spent.
	Position float64 `json:"position,omitempty"`
	// InLastThird marks a first judgment that arrived in the last third
	// of the trajectory.
	InLastThird bool `json:"in_last_third,omitempty"`
}

// Lineage is one task's chain: the goal revisions and the sessions that
// worked on them.
//
// A judgment that one session made does not stay in that session. The same
// goal moves from one agent to another, and the lineage is what says which
// judgment belongs to which revision of which session's work.
type Lineage struct {
	TaskID    string            `json:"task_id"`
	Revisions []LineageRevision `json:"revisions"`
	Sessions  []LineageSession  `json:"sessions"`
}

// Lineage returns the chain of a task.
func (d *DB) Lineage(taskID string) (Lineage, error) {
	l := Lineage{TaskID: taskID}
	rows, err := d.Query(`SELECT revision,payload FROM intent_revision WHERE task_id=? ORDER BY revision`, taskID)
	if err != nil {
		return l, err
	}
	for rows.Next() {
		var number int64
		var payload string
		if err := rows.Scan(&number, &payload); err != nil {
			rows.Close()
			return l, err
		}
		var rev intent.Revision
		if err := json.Unmarshal([]byte(payload), &rev); err != nil {
			rows.Close()
			return l, err
		}
		l.Revisions = append(l.Revisions, LineageRevision{Revision: uint64(number), Goal: rev.Goal, At: rev.Source.At})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return l, err
	}
	sessions, err := d.TaskSessions(taskID)
	if err != nil {
		return l, err
	}
	for _, link := range sessions {
		s := LineageSession{Agent: link.Agent, Session: link.SessionID,
			ParentAgent: link.ParentAgent, ParentSession: link.ParentSessionID}
		judgments, err := d.sessionJudgments(link.SessionID)
		if err != nil {
			return l, err
		}
		s.Judgments = judgments
		s.Verdicts = len(judgments)
		if len(judgments) > 0 {
			s.FirstVerdict = judgments[0].At
		}
		if at, err := d.LastActivity(link.SessionID); err == nil {
			s.LastActivity = at
		}
		if !s.FirstVerdict.IsZero() && !s.LastActivity.IsZero() && s.LastActivity.After(s.FirstVerdict) {
			s.LeadMS = s.LastActivity.Sub(s.FirstVerdict).Milliseconds()
		}
		timing, err := d.DetectionTiming(link.SessionID)
		if err == nil {
			s.Position = timing.Position
			s.InLastThird = timing.InLastThird
		}
		l.Sessions = append(l.Sessions, s)
	}
	return l, nil
}

// sessionJudgments lists a session's judgments in the order they were
// raised.
//
// The block mark comes from the intervention table: a judgment that was
// enforced and a judgment that was only announced are different facts,
// and the lineage says which one happened.
func (d *DB) sessionJudgments(sessionID string) ([]LineageVerdict, error) {
	rows, err := d.Query(`SELECT v.rule, v.detector, v.level, v.seq, COALESCE(e.ts,''),
		EXISTS(SELECT 1 FROM intervention i WHERE i.verdict_id=v.id AND i.blocked=1)
		FROM verdict v LEFT JOIN event e ON e.session_id=v.session_id AND e.seq=v.seq
		WHERE v.session_id=? ORDER BY v.seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LineageVerdict
	for rows.Next() {
		var v LineageVerdict
		var at string
		var blocked int
		if err := rows.Scan(&v.Rule, &v.Detector, &v.Level, &v.Seq, &at, &blocked); err != nil {
			return nil, err
		}
		if at != "" {
			v.At, _ = time.Parse(time.RFC3339Nano, at)
		}
		v.Blocked = blocked == 1
		out = append(out, v)
	}
	return out, rows.Err()
}
