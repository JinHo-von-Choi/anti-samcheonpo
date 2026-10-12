package ledger

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestObservationsSurviveReopenAndExcludeTransientText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r := result(t)
	if _, err = db.SaveAnalysis(r, 1, 2, nil); err != nil {
		t.Fatal(err)
	}
	ev := r.Session.Events[0]
	ev.Cmd = "secret-command"
	ev.Text = "secret-output"
	ev.Purpose = "secret-purpose"
	ev.BehaviorTarget = "targethash"
	ev.Reliability = &event.Reliability{TaskID: "task", Revision: 1, Complete: true}
	ev.Decisions = []event.Decision{{Rule: "s2.behavior_loop", Code: "environment_unknown", Evidence: []int64{ev.Seq}}, {Rule: "bad", Code: "secret-output"}}
	ev.WorkProgress.Knowledge = true
	ev.ResearchSources = []event.ResearchSource{{ReferenceHash: "source", HostHash: "host"}}
	ev.ResearchLinks = []event.ResearchLink{{Claim: ev.Seq, SourceHash: "source", Evidence: 0, Kind: "source_link", Meaning: "observed_source_link_not_verified_claim"}}
	if err = db.SaveEventObservation(r.Session.ID, ev); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err = db.QueryRow(`SELECT payload FROM event_observation WHERE session_id=? AND seq=?`, r.Session.ID, ev.Seq).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-command", "secret-output", "secret-purpose"} {
		if strings.Contains(payload, secret) {
			t.Fatal("transient text persisted")
		}
	}
	db.Close()
	db, err = OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	evs, err := db.EventsOf(r.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := evs[0]
	if !reflect.DeepEqual(got.ResearchLinks, ev.ResearchLinks) || !reflect.DeepEqual(got.ResearchSources, ev.ResearchSources) {
		t.Fatal("research provenance lost on reopen")
	}
	if got.BehaviorTarget != "targethash" || !got.WorkProgress.Knowledge || !reflect.DeepEqual(got.Reliability, ev.Reliability) || len(got.Decisions) != 1 {
		t.Fatalf("%+v", got)
	}
	if _, err = db.Exec(`DELETE FROM event_observation`); err == nil {
		t.Fatal("diagnostic connection allows mutations")
	}
}
func TestReadOnlyDoesNotCreateLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	db, err := OpenReadOnly(path)
	if err == nil {
		db.Close()
		t.Fatal("missing DB accepted")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("diagnostic created DB")
	}
}
