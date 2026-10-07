package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/judge"
)

func (d *DB) JudgeBudget(agent, session string) (judge.Budget, error) {
	var version, raw string
	var b judge.Budget
	err := d.QueryRow(`SELECT version,payload FROM judge_budget WHERE agent=? AND session_id=?`, agent, session).Scan(&version, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	if version != "judge-budget/1" {
		return b, errors.New("unsupported judge budget version")
	}
	if err = json.Unmarshal([]byte(raw), &b); err != nil {
		return b, err
	}
	return b, b.Validate()
}

func (d *DB) SaveJudgeBudget(agent, session string, b judge.Budget) error {
	if agent == "" || session == "" {
		return errors.New("judge budget identity required")
	}
	if err := b.Validate(); err != nil {
		return err
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw, version string
	err = tx.QueryRow(`SELECT version,payload FROM judge_budget WHERE agent=? AND session_id=?`, agent, session).Scan(&version, &raw)
	if err == nil {
		var old judge.Budget
		if version != "judge-budget/1" {
			return errors.New("unsupported judge budget version")
		}
		if err = json.Unmarshal([]byte(raw), &old); err != nil {
			return err
		}
		if err = old.Validate(); err != nil {
			return err
		}
		if b.Calls < old.Calls || b.SpentMicro < old.SpentMicro || old.Unknown && !b.Unknown || old.Overrun && !b.Overrun {
			return errors.New("judge budget cannot move backwards")
		}
		for key := range old.Seen {
			if !b.Seen[key] {
				return errors.New("judge budget lost request identity")
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	payload, err := json.Marshal(b)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO judge_budget(agent,session_id,version,payload) VALUES(?,?,?,?) ON CONFLICT(agent,session_id) DO UPDATE SET payload=excluded.payload`, agent, session, "judge-budget/1", string(payload))
	if err != nil {
		return err
	}
	return tx.Commit()
}
