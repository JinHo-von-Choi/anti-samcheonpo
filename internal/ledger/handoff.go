package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/handoff"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
)

// HandoffUsage returns observed event totals, not a complete invoice. Agent is
// checked against the legacy session row to reject cross-agent ID collisions.
func (d *DB) HandoffUsage(agent, session string) (handoff.Usage, error) {
	u := handoff.Usage{Session: handoff.SessionRef{Agent: agent, ID: session}}
	var storedAgent string
	err := d.QueryRow(`SELECT agent FROM session WHERE id=?`, session).Scan(&storedAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return u, nil
	}
	if err != nil {
		return u, err
	}
	if storedAgent != agent {
		return u, nil
	}
	var tokens, micro, unpriced, unknownJudge int64
	err = d.QueryRow(`SELECT COALESCE(SUM(COALESCE(tokens_in,0)+COALESCE(tokens_out,0)+COALESCE(tokens_cache_read,0)+COALESCE(tokens_cache_write,0)),0), COALESCE(SUM(cost_micro_krw),0),COALESCE(SUM(CASE WHEN priced=0 THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN tool='judge' AND COALESCE(tokens_in,0)+COALESCE(tokens_out,0)=0 THEN 1 ELSE 0 END),0) FROM event WHERE session_id=?`, session).Scan(&tokens, &micro, &unpriced, &unknownJudge)
	if err != nil {
		return u, err
	}
	if tokens > 0 {
		u.Tokens = &tokens
		if unpriced == 0 && unknownJudge == 0 {
			u.APIEquivalentMicro = &micro
		}
	}
	return u, nil
}

func (d *DB) FailedApproaches(agent, session string) ([]handoff.FailedApproach, error) {
	rows, err := d.Query(`SELECT e.seq,COALESCE(e.tool,''),COALESCE(e.cmd_fp,''),COALESCE(e.result_fp,''),COALESCE(e.summary,''),e.exit_code FROM event e JOIN session s ON s.id=e.session_id WHERE s.agent=? AND s.id=? AND e.exit_code>0 AND e.kind='tool' ORDER BY e.seq DESC LIMIT 20`, agent, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []handoff.FailedApproach
	for rows.Next() {
		var f handoff.FailedApproach
		if err := rows.Scan(&f.Seq, &f.Tool, &f.CommandHash, &f.ResultHash, &f.Summary, &f.ExitCode); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (d *DB) TaskRevisions(taskID string) ([]intent.Revision, error) {
	rows, err := d.Query(`SELECT payload FROM intent_revision WHERE task_id=? ORDER BY revision`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []intent.Revision
	for rows.Next() {
		var payload string
		var r intent.Revision
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		if err := r.Validate(); err != nil {
			return nil, err
		}
		if r.TaskID != taskID || r.Number != uint64(len(out)+1) {
			return nil, errors.New("inconsistent intent history")
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) TaskEvidence(taskID string, revision uint64) ([]verification.Evidence, error) {
	rows, err := d.Query(`SELECT payload FROM verification_evidence WHERE task_id=? AND revision=? ORDER BY rowid`, taskID, revision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []verification.Evidence
	for rows.Next() {
		var payload string
		var e verification.Evidence
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &e); err != nil {
			return nil, err
		}
		if err := e.Validate(); err != nil {
			return nil, err
		}
		if e.Key.TaskID != taskID || e.Key.Revision != revision {
			return nil, errors.New("inconsistent evidence identity")
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (d *DB) TaskSessions(taskID string) ([]intent.SessionLink, error) {
	rows, err := d.Query(`SELECT payload FROM task_session WHERE task_id=? ORDER BY agent,session_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []intent.SessionLink
	for rows.Next() {
		var payload string
		var link intent.SessionLink
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &link); err != nil {
			return nil, err
		}
		if link.TaskID != taskID || link.Agent == "" || link.SessionID == "" {
			return nil, errors.New("inconsistent task session link")
		}
		out = append(out, link)
	}
	return out, rows.Err()
}
