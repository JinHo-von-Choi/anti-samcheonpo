package ledger

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestStoredResultPreservesAnalysisAndBilling(t *testing.T) {
	for _, zero := range []bool{false, true} {
		t.Run(map[bool]string{false: "priced", true: "observed-zero"}[zero], func(t *testing.T) {
			db, err := Open(filepath.Join(t.TempDir(), "l.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			r := result(t)
			r.Session.SourcePath = ""
			r.Session.QuotaKnown, r.Session.QuotaWindowMin = true, 300
			r.Session.QuotaUsedPct = 0
			if zero {
				r.Session.Events = []*event.Event{{SessionID: r.Session.ID, Seq: 1, Kind: event.KindMessage, Priced: true, Bucket: analyze.BucketOther}}
				r.Session.UsageLines, r.Session.UsageParsed = 1, 1
				r.Verdicts = nil
				r.Totals = analyze.Totals{BucketMicro: map[string]int64{analyze.BucketOther: 0}, BucketTokens: map[string]int64{analyze.BucketOther: 0}}
			}
			// These canonical inputs were missing from the old event reader.
			ev := r.Session.Events[0]
			ev.WriteHashes = map[string]string{"/project/file": "hash"}
			ev.ErrFPs, ev.FailedTests = []string{"error-hash"}, []string{"test-name"}
			ev.Usage.CacheWrite1h = ev.Usage.CacheWrite
			ev.Cmd, ev.CmdNorm, ev.Text = "transient-command", "transient-normalized", "transient-output"
			r.Session.FirstPrompt = "transient-prompt"
			wantSeal, err := db.SaveAnalysis(r, 1, 2, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, gotSeal, err := db.StoredResult(r.Session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(wantSeal, gotSeal) || !reflect.DeepEqual(r.Totals, got.Totals) || r.Grade != got.Grade {
				t.Fatal("stored analysis or seal changed")
			}
			before := cost.Measure(cost.ObserveSession(r.Session, r.PriceVersion), r.Totals.Micro, r.Totals.Tokens, r.Totals.UnpricedTokens)
			after := cost.Measure(cost.ObserveSession(got.Session, got.PriceVersion), got.Totals.Micro, got.Totals.Tokens, got.Totals.UnpricedTokens)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("billing changed: before=%+v after=%+v", before, after)
			}
			var payload string
			if err := db.QueryRow(`SELECT payload FROM receipt_snapshot WHERE session_id=?`, r.Session.ID).Scan(&payload); err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"transient-command", "transient-normalized", "transient-output", "transient-prompt"} {
				if strings.Contains(payload, secret) {
					t.Fatalf("snapshot stored transient text: %s", secret)
				}
			}
		})
	}
}

func TestStoredResultUsesCompletedSnapshotAndRejectsReopenedSession(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := result(t)
	r.Session.SourcePath = ""
	sl, err := db.SaveAnalysis(r, 1, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	ev := r.Session.Events[0]
	ev.Usage.In++
	if err := db.UpdateLiveCost(r.Session.ID, ev); err != nil {
		t.Fatal(err)
	}
	got, seal, err := db.StoredResult(r.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Totals.Tokens != sl.TotalTokens || seal.Head != sl.Head {
		t.Fatal("mixed current rows with completed seal")
	}
	if err := db.startObservation(r.Session.ID, r.Session.Agent); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.StoredResult(r.Session.ID); err == nil {
		t.Fatal("issued completed receipt for reopened session")
	}
	if _, err := db.SaveAnalysis(result(t), 1, 2, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.StoredResult(r.Session.ID); err != nil {
		t.Fatal(err)
	}
}

func TestStoredResultRejectsCorruptSnapshot(t *testing.T) {
	for _, corruption := range []string{"event", "seal", "missing-seal", "json"} {
		t.Run(corruption, func(t *testing.T) {
			db, err := Open(filepath.Join(t.TempDir(), "l.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			r := result(t)
			if _, err := db.SaveAnalysis(r, 1, 2, nil); err != nil {
				t.Fatal(err)
			}
			switch corruption {
			case "event":
				payload, err := encodeSnapshot(r)
				if err != nil {
					t.Fatal(err)
				}
				var snap receiptSnapshot
				if err := json.Unmarshal(payload, &snap); err != nil {
					t.Fatal(err)
				}
				snap.Events[0].Usage.In++
				payload, err = json.Marshal(snap)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec(`UPDATE receipt_snapshot SET payload=?`, string(payload))
				if err != nil {
					t.Fatal(err)
				}
			case "seal":
				_, err = db.Exec(`UPDATE session_seal SET seal='{}'`)
			case "missing-seal":
				_, err = db.Exec(`DELETE FROM session_seal`)
			case "json":
				_, err = db.Exec(`UPDATE receipt_snapshot SET payload='{'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := db.StoredResult(r.Session.ID); err == nil {
				t.Fatal("corrupt snapshot accepted")
			}
		})
	}
}

func TestStoredResultLegacyDoesNotClaimSealVerification(t *testing.T) {
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
	got, sl, err := db.StoredResult(r.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sl.Head != "" || got.Totals.Tokens != r.Totals.Tokens {
		t.Fatal("legacy snapshot claims verification or changed totals")
	}
	if _, err := db.Exec(`UPDATE event SET tokens_in=tokens_in+1 WHERE seq=1`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.StoredResult(r.Session.ID); err == nil {
		t.Fatal("legacy totals mismatch accepted")
	}
}

func TestSnapshotEncodingFailureRollsBackAnalysis(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := result(t)
	want, err := db.SaveAnalysis(r, 1, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Session.Events[0].Usage.In++
	r.Verdicts = append(r.Verdicts, detect.Signal{ID: "invalid", Facts: map[string]any{"invalid": make(chan int)}})
	if _, err := db.SaveAnalysis(r, 1, 2, nil); err == nil {
		t.Fatal("unserializable snapshot accepted")
	}
	got, sl, err := db.StoredResult(r.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sl, want) || got.Totals.Tokens != want.TotalTokens {
		t.Fatal("failed save replaced completed analysis")
	}
	rows, err := db.EventsOf(r.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Usage.In != r.Session.Events[0].Usage.In-1 {
		t.Fatal("failed save changed mutable event rows")
	}
}
