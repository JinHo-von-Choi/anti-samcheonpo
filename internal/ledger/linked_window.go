package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/assessment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"time"
)

// FilePath is resolved once at startup, never on a pre-tool hook.
func (d *DB) FilePath() (string, error) {
	rows, err := d.Query(`PRAGMA database_list`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, path string
		if err = rows.Scan(&seq, &name, &path); err != nil {
			return "", err
		}
		if name == "main" && path != "" {
			return path, nil
		}
	}
	return "", errors.New("file-backed ledger unavailable")
}

// LinkedWindow reads an atomic bounded snapshot. Live callers use a separate
// read-only connection, so background diagnostics do not occupy the writer.
func (d *DB) LinkedWindow(ctx context.Context, task string, revision uint64, maxSessions, perSession int) ([]assessment.TaskNode, bool, error) {
	if maxSessions < 1 || maxSessions > 64 || perSession < 1 || perSession > 512 {
		return nil, false, errors.New("invalid linked window")
	}
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var current uint64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision),0) FROM intent_revision WHERE task_id=?`, task).Scan(&current); err != nil {
		return nil, false, err
	}
	if current != revision {
		return nil, false, errors.New("linked revision changed")
	}
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM task_session WHERE task_id=? ORDER BY agent,session_id LIMIT ?`, task, maxSessions+1)
	if err != nil {
		return nil, false, err
	}
	var links []intent.SessionLink
	for rows.Next() {
		var raw string
		var link intent.SessionLink
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, false, err
		}
		if err = json.Unmarshal([]byte(raw), &link); err != nil {
			rows.Close()
			return nil, false, err
		}
		if link.TaskID != task || link.Agent == "" || link.SessionID == "" {
			rows.Close()
			return nil, false, errors.New("invalid linked identity")
		}
		links = append(links, link)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	if len(links) > maxSessions {
		return nil, true, nil
	}
	var out []assessment.TaskNode
	truncated := false
	for _, link := range links {
		var agent string
		if err = tx.QueryRowContext(ctx, `SELECT agent FROM session WHERE id=?`, link.SessionID).Scan(&agent); errors.Is(err, sql.ErrNoRows) {
			out = append(out, assessment.TaskNode{Link: link})
			continue
		} else if err != nil {
			return nil, false, err
		}
		if agent != link.Agent {
			return nil, false, errors.New("linked agent mismatch")
		}
		rows, err = tx.QueryContext(ctx, `SELECT e.seq,COALESCE(e.ts,''),e.kind,COALESCE(e.tool,''),COALESCE(e.category,''),e.exit_code,COALESCE(o.payload,''),COALESCE(e.tokens_in,0),COALESCE(e.tokens_out,0),COALESCE(e.tokens_cache_read,0),COALESCE(e.tokens_cache_write,0) FROM event e LEFT JOIN event_observation o ON o.session_id=e.session_id AND o.seq=e.seq WHERE e.session_id=? ORDER BY e.seq DESC LIMIT ?`, link.SessionID, perSession+1)
		if err != nil {
			return nil, false, err
		}
		n := assessment.TaskNode{Link: link}
		for rows.Next() {
			ev := &event.Event{SessionID: link.SessionID}
			var at, kind, cat, raw string
			var exit sql.NullInt64
			if err = rows.Scan(&ev.Seq, &at, &kind, &ev.Tool, &cat, &exit, &raw, &ev.Usage.In, &ev.Usage.Out, &ev.Usage.CacheRead, &ev.Usage.CacheWrite); err != nil {
				rows.Close()
				return nil, false, err
			}
			ev.TS, _ = time.Parse(time.RFC3339Nano, at)
			ev.Kind = event.Kind(kind)
			ev.Category = event.Category(cat)
			if exit.Valid {
				x := int(exit.Int64)
				ev.ExitCode = &x
			}
			if raw != "" {
				var p observationPayload
				if err = json.Unmarshal([]byte(raw), &p); err != nil {
					rows.Close()
					return nil, false, err
				}
				applyObservation(ev, p)
			}
			n.Events = append(n.Events, ev)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, false, err
		}
		if len(n.Events) > perSession {
			n.Events = n.Events[:perSession]
			truncated = true
		}
		for i, j := 0, len(n.Events)-1; i < j; i, j = i+1, j-1 {
			n.Events[i], n.Events[j] = n.Events[j], n.Events[i]
		}
		out = append(out, n)
	}
	return out, truncated, tx.Commit()
}
