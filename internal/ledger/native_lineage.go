package ledger

import (
	"database/sql"
	"errors"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"sort"
	"time"
)

// BindSessionParent stores host-declared lineage without inheriting goals,
// approvals or acceptance. A child identity cannot be reparented or cycled.
func (d *DB) BindSessionParent(agent, child, parent, project, source string) error {
	if agent == "" || child == "" || parent == "" || child == parent || project == "" || source == "" {
		return errors.New("invalid session lineage")
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var parentRoot string
	err = tx.QueryRow(`SELECT COALESCE(project_path,'') FROM session WHERE agent=? AND id=?`, agent, parent).Scan(&parentRoot)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if parentRoot != "" && !intent.MatchesProject(project, parentRoot) {
		return errors.New("session parent belongs to another project")
	}
	current := parent
	for depth := 0; current != ""; depth++ {
		if current == child || depth >= 128 {
			return errors.New("session lineage cycle or depth overflow")
		}
		var next, root string
		err = tx.QueryRow(`SELECT parent_session_id,project_id FROM session_lineage WHERE agent=? AND session_id=?`, agent, current).Scan(&next, &root)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		if root != project {
			return errors.New("session lineage crosses projects")
		}
		current = next
	}
	var oldParent, oldProject string
	err = tx.QueryRow(`SELECT parent_session_id,project_id FROM session_lineage WHERE agent=? AND session_id=?`, agent, child).Scan(&oldParent, &oldProject)
	if err == nil {
		if oldParent != parent || oldProject != project {
			return errors.New("session lineage identity conflict")
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(`INSERT INTO session_lineage(agent,session_id,parent_session_id,project_id,source,observed_at) VALUES(?,?,?,?,?,?)`, agent, child, parent, project, source, ts(time.Now()))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) SessionParent(agent, child, project string) (string, error) {
	var parent string
	err := d.QueryRow(`SELECT parent_session_id FROM session_lineage WHERE agent=? AND session_id=? AND project_id=?`, agent, child, project).Scan(&parent)
	return parent, err
}

// NativeSessionTree returns host-declared descendants in parent-first order.
// It never equates their task goals or acceptance evidence.
func (d *DB) NativeSessionTree(agent, session, project string) ([]NativeSessionLink, error) {
	rows, err := d.Query(`SELECT session_id,parent_session_id FROM session_lineage WHERE agent=? AND project_id=? LIMIT 4097`, agent, project)
	if err != nil {
		return nil, err
	}
	parents := map[string]string{}
	for rows.Next() {
		var child, parent string
		if err := rows.Scan(&child, &parent); err != nil {
			rows.Close()
			return nil, err
		}
		parents[child] = parent
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(parents) > 4096 {
		return nil, errors.New("native lineage exceeds bounded restore window")
	}
	rootOf := func(id string) (string, int) {
		depth := 0
		for parents[id] != "" && depth < 128 {
			id = parents[id]
			depth++
		}
		return id, depth
	}
	root, _ := rootOf(session)
	var out []NativeSessionLink
	for child, parent := range parents {
		candidate, depth := rootOf(child)
		if candidate == root {
			out = append(out, NativeSessionLink{Session: child, Parent: parent, Depth: depth})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Depth != out[j].Depth {
			return out[i].Depth < out[j].Depth
		}
		return out[i].Session < out[j].Session
	})
	return out, nil
}

type NativeSessionLink struct {
	Session string `json:"session_id"`
	Parent  string `json:"parent_session_id"`
	Depth   int    `json:"depth"`
}

func (d *DB) ObservedSessionSpend(session string) (int64, error) {
	var total int64
	err := d.QueryRow(`SELECT COALESCE(sum(cost_micro_krw),0) FROM event WHERE session_id=?`, session).Scan(&total)
	return total, err
}
