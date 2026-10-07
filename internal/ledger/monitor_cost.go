package ledger

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/judge"
)

// MonitoringCost reads all judge reservations in a dedicated experiment home.
// It must not be used for a shared home: every session is included exactly once.
// A reservation without a settled measurement is not a measured zero.
func MonitoringCost(path string) (int64, error) {
	db, err := openReadOnly(path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	if _, err := inspectSchema(db); err != nil {
		return 0, err
	}
	var gaps int64
	if err := db.QueryRow(`SELECT COALESCE(SUM(rejected),0) FROM observation_gap`).Scan(&gaps); err != nil {
		return 0, err
	}
	if gaps > 0 {
		return 0, fmt.Errorf("monitor observation gap")
	}
	rows, err := db.Query(`SELECT version,payload FROM judge_budget`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var total int64
	for rows.Next() {
		var version, raw string
		var b judge.Budget
		if err := rows.Scan(&version, &raw); err != nil {
			return 0, err
		}
		if version != "judge-budget/1" || json.Unmarshal([]byte(raw), &b) != nil || b.Validate() != nil {
			return 0, fmt.Errorf("invalid monitoring budget")
		}
		if b.Pending || b.Unknown || b.ReservedMicro > 0 {
			return 0, fmt.Errorf("monitoring cost incomplete")
		}
		if b.SpentMicro > math.MaxInt64-total {
			return 0, fmt.Errorf("monitoring cost overflow")
		}
		total += b.SpentMicro
	}
	return total, rows.Err()
}
