package eval

import (
	"encoding/json"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"os"
	"path/filepath"
	"testing"
)

func TestReviewFreezeIncludesContentIdentityAndChecksAndNoLabels(t *testing.T) {
	e := &event.Event{Seq: 1, ObservationFP: "first", ResultObserved: true}
	old := ReviewSourceDigest([]*event.Event{e})
	e.ObservationFP = "second"
	if old == ReviewSourceDigest([]*event.Event{e}) {
		t.Fatal("changed source content retained frozen identity")
	}
	q := ReviewQueue{Version: "review-queue/2", Candidates: 1, Items: []ReviewItem{{Session: "s", Start: 1, End: 2, Stratum: "candidate", SourceDigest: old}}}
	path := filepath.Join(t.TempDir(), "queue.json")
	write := func() {
		q.Digest = ReviewQueueDigest(q)
		b, _ := json.Marshal(q)
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if _, err := LoadReviewQueue(path); err != nil {
		t.Fatal(err)
	}
	q.LabelsCreated = true
	write()
	if _, err := LoadReviewQueue(path); err == nil {
		t.Fatal("fabricated labels flag accepted")
	}
	q.LabelsCreated = false
	q.Items = append(q.Items, q.Items[0])
	q.Candidates = 2
	write()
	if _, err := LoadReviewQueue(path); err == nil {
		t.Fatal("duplicate session counted twice")
	}
}
