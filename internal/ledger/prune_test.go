package ledger

import (
	"path/filepath"
	"testing"
	"time"
)

// ageObservation makes a session look like it was closed long ago.
func ageObservation(t *testing.T, db *DB, id string, at time.Time) {
	t.Helper()
	if _, err := db.Exec(`UPDATE session_observation SET closed_at=?, close_reason=? WHERE session_id=?`,
		at.UTC().Format(time.RFC3339Nano), ObservationSessionEnd, id); err != nil {
		t.Fatal(err)
	}
}

// The retention pass removes the event-level detail of old sessions. The
// seal and the receipt must survive it, because a receipt that cannot be
// verified against its seal is worth less than the space it saved.
func TestPruneKeepsSealAndReceipt(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := result(t)
	if _, err := db.SaveAnalysis(r, 1, 2, nil); err != nil {
		t.Fatal(err)
	}
	ageObservation(t, db, r.Session.ID, time.Now().AddDate(0, 0, -PruneDays-1))

	pr, err := db.Prune(time.Now().AddDate(0, 0, -PruneDays))
	if err != nil {
		t.Fatal(err)
	}
	if pr.Sessions != 1 || pr.Events == 0 {
		t.Fatalf("prune report = %+v", pr)
	}
	events, err := db.EventsOf(r.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("prune left %d event rows", len(events))
	}
	// The receipt still verifies against the seal that was made with it.
	got, sl, err := db.StoredResult(r.Session.ID)
	if err != nil {
		t.Fatalf("receipt lost after prune: %v", err)
	}
	if sl.Spec == "" || got.Session == nil || got.Session.ID != r.Session.ID {
		t.Fatal("stored receipt did not survive the prune")
	}
	if len(got.Verdicts) != len(r.Verdicts) {
		t.Fatalf("verdicts changed after prune: %d != %d", len(got.Verdicts), len(r.Verdicts))
	}
}

// A session that is still being observed is not a candidate. Pruning it
// would remove evidence from a session that is still running.
func TestPruneKeepsObservedSession(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := result(t)
	if _, err := db.SaveAnalysis(r, 1, 2, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.startObservation(r.Session.ID, r.Session.Agent); err != nil {
		t.Fatal(err)
	}
	pr, err := db.Prune(time.Now().AddDate(0, 0, -PruneDays))
	if err != nil {
		t.Fatal(err)
	}
	if pr.Sessions != 0 {
		t.Fatalf("prune touched %d observed sessions", pr.Sessions)
	}
	events, err := db.EventsOf(r.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("prune removed the events of an observed session")
	}
}

// A session whose analysis was never stored keeps its events. They are the
// only copy of what was observed, and there would be nothing left to show
// for them.
func TestPruneKeepsSessionWithoutStoredAnalysis(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := result(t)
	if _, err := db.SaveAnalysis(r, 1, 2, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM receipt_snapshot`); err != nil {
		t.Fatal(err)
	}
	ageObservation(t, db, r.Session.ID, time.Now().AddDate(0, 0, -PruneDays-1))

	pr, err := db.Prune(time.Now().AddDate(0, 0, -PruneDays))
	if err != nil {
		t.Fatal(err)
	}
	if pr.Sessions != 0 {
		t.Fatalf("prune touched %d sessions without a stored analysis", pr.Sessions)
	}
	events, err := db.EventsOf(r.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("prune removed the only copy of the observed events")
	}
}
