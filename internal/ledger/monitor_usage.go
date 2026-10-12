package ledger

import (
	"encoding/json"
	"errors"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/judge"
)

// MonitoringUsage requires a finalized dedicated home and one recorded usage
// event for every reserved judge call. Missing events cannot imply zero tokens.
func MonitoringUsage(path string) (event.Usage, error) {
	var usage event.Usage
	db, err := OpenReadOnly(path)
	if err != nil {
		return usage, err
	}
	defer db.Close()
	if _, err = inspectSchema(db.DB); err != nil {
		return usage, err
	}
	var gaps int64
	if err = db.QueryRow(`SELECT COALESCE(SUM(rejected),0) FROM observation_gap`).Scan(&gaps); err != nil {
		return usage, err
	}
	if gaps > 0 {
		return usage, errors.New("monitor observation gap")
	}
	rows, err := db.Query(`SELECT version,payload FROM judge_budget`)
	if err != nil {
		return usage, err
	}
	calls := 0
	for rows.Next() {
		var version, raw string
		var budget judge.Budget
		if err = rows.Scan(&version, &raw); err != nil {
			rows.Close()
			return usage, err
		}
		if version != "judge-budget/1" || json.Unmarshal([]byte(raw), &budget) != nil || budget.Validate() != nil {
			rows.Close()
			return usage, errors.New("invalid monitoring budget")
		}
		if budget.Pending {
			rows.Close()
			return usage, errors.New("monitoring call still pending")
		}
		calls += budget.Calls
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return usage, err
	}
	var events int
	err = db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(tokens_in),0),COALESCE(SUM(tokens_out),0),COALESCE(SUM(tokens_cache_read),0),COALESCE(SUM(tokens_cache_write),0) FROM event WHERE tool='judge'`).Scan(&events, &usage.In, &usage.Out, &usage.CacheRead, &usage.CacheWrite)
	if err != nil {
		return usage, err
	}
	if events != calls || calls > 0 && usage.Total() <= 0 || usage.In < 0 || usage.Out < 0 || usage.CacheRead < 0 || usage.CacheWrite < 0 {
		return usage, errors.New("monitoring token coverage incomplete")
	}
	return usage, nil
}
