package bench

import "github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"

// An empty daemon ledger is not evidence that a trial was monitored. Read the
// agent's exact session after shutdown, independently of monitor token cost.
func monitorObservation(path, session string) (events, delivered int, problem string) {
	db, err := ledger.OpenReadOnly(path)
	if err != nil {
		return 0, 0, err.Error()
	}
	defer db.Close()
	if err = db.QueryRow(`SELECT COUNT(*) FROM event WHERE session_id=?`, session).Scan(&events); err != nil {
		return 0, 0, err.Error()
	}
	if events == 0 {
		return 0, 0, "monitor received no events for the agent session"
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM intervention WHERE session_id=? AND delivery_state='delivered'`, session).Scan(&delivered); err != nil {
		return events, 0, err.Error()
	}
	return events, delivered, ""
}
