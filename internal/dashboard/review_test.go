package dashboard

import (
	"encoding/json"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"strings"
	"testing"
)

func TestFrozenReviewHidesStrataAndRejectsChangedSourceOrDuplicate(t *testing.T) {
	s, _ := dashboardFixture(t)
	_, events, err := s.load("fixture-session", true)
	if err != nil {
		t.Fatal(err)
	}
	q := eval.ReviewQueue{Version: "review-queue/2", Candidates: 1, Items: []eval.ReviewItem{{Session: "fixture-session", Start: 0, End: 2, Stratum: "candidate", SourceDigest: eval.ReviewSourceDigest(events)}}}
	q.Digest = eval.ReviewQueueDigest(q)
	s.queue = &q
	w := perform(s, "GET", "/api/review-queue", "", true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "sampling_stratum") || strings.Contains(w.Body.String(), "candidate") {
		t.Fatal("review reveals detector opinion", w.Body)
	}
	label := map[string]any{"session": "fixture-session", "labeler": "human-a", "start": 0, "end": 2, "symptom": "S4", "justified": "yes", "outcome": "unknown", "queue_digest": q.Digest}
	raw, _ := json.Marshal(label)
	w = perform(s, "POST", "/api/label", string(raw), true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	saved, err := eval.LoadLabels(s.LabelsDir)
	if err != nil || len(saved) != 1 || saved[0].ReviewSourceDigest != q.Items[0].SourceDigest || saved[0].ReviewQueueDigest != q.Digest {
		t.Fatalf("review provenance missing: %v %v", saved, err)
	}
	w = perform(s, "POST", "/api/label", string(raw), true)
	if w.Code == 200 {
		t.Fatal("duplicate label accepted")
	}
	label["labeler"] = "human-b"
	raw, _ = json.Marshal(label)
	q.Items[0].SourceDigest = "changed"
	w = perform(s, "POST", "/api/label", string(raw), true)
	if w.Code == 200 {
		t.Fatal("changed source accepted")
	}
}
