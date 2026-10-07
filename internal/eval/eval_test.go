package eval

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestWilson(t *testing.T) {
	ci := Wilson(8, 10)
	if math.Abs(ci.Lo-0.4902) > 0.001 || math.Abs(ci.Hi-0.9433) > 0.001 {
		t.Errorf("Wilson(8,10) = %+v", ci)
	}
	if z := Wilson(0, 0); z.Lo != 0 || z.Hi != 1 {
		t.Errorf("empty interval %+v", z)
	}
}

func TestCohen(t *testing.T) {
	same := [][2]string{{"S1", "S1"}, {"none", "none"}, {"S2", "S2"}}
	if Cohen(same) != 1 {
		t.Error("perfect agreement is 1")
	}
	// classic 2x2 example: po=0.7, pe=0.5 -> 0.4
	var p [][2]string
	add := func(a, b string, n int) {
		for i := 0; i < n; i++ {
			p = append(p, [2]string{a, b})
		}
	}
	add("y", "y", 20)
	add("y", "n", 5)
	add("n", "y", 10)
	add("n", "n", 15)
	if k := Cohen(p); math.Abs(k-0.4) > 1e-9 {
		t.Errorf("kappa %v", k)
	}
}

func TestSplitOverlapIsError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "split.json")
	_ = os.WriteFile(p, []byte(`{"calibration":["a","b"],"holdout":["b","c"]}`), 0o644)
	if _, err := LoadSplit(p); err == nil {
		t.Fatal("a session in both sets must be rejected")
	}
	_ = os.WriteFile(p, []byte(`{"calibration":["a"],"holdout":["c"]}`), 0o644)
	if _, err := LoadSplit(p); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluate(t *testing.T) {
	var evs []*event.Event
	for i := int64(0); i < 20; i++ {
		evs = append(evs, &event.Event{Seq: i, CostMicroKRW: 1_000_000})
	}
	data := map[string]SessionData{"s": {Events: evs, Verdicts: []detect.Signal{
		{Detector: "S1", Seq: 6, Evidence: []int64{4, 5, 6}, Confidence: 0.95}, // hits the S1 label
		{Detector: "S1", Seq: 15, Evidence: []int64{15}, Confidence: 0.95},     // false positive
		{Detector: "S4", Seq: 2, Evidence: []int64{2}, Confidence: 0.3},        // below confidence floor, ignored
		{Detector: "S2", Seq: 9, Evidence: []int64{9}, Confidence: 0.9, Suppressed: true},
	}}}
	labels := []Label{
		{Session: "s", Start: 3, End: 6, Symptom: "S1", Labeler: "a"},
		{Session: "s", Start: 10, End: 12, Symptom: "S2", Labeler: "a"},
		{Session: "s", Start: 3, End: 6, Symptom: "S1", Labeler: "b"},
	}
	rep := Evaluate("holdout", []string{"s"}, data, labels)
	var s1, s2 SymptomScore
	for _, s := range rep.Symptoms {
		switch s.Symptom {
		case "S1":
			s1 = s
		case "S2":
			s2 = s
		}
	}
	if s1.Detections != 2 || s1.Correct != 1 || s1.Precision != 0.5 {
		t.Errorf("S1 precision %+v", s1)
	}
	if s1.Positives != 1 || s1.Covered != 1 || s1.Recall != 1 {
		t.Errorf("S1 recall %+v (two labelers marking one interval are one incident)", s1)
	}
	if s1.LatencyMedianKRW != 4 {
		t.Errorf("detection latency cost %d, want 4 won (events 3..6)", s1.LatencyMedianKRW)
	}
	if s2.Positives != 0 || rep.Disputed != 1 || rep.Incidents != 1 {
		t.Errorf("an S2 span one of two labelers marked is disputed, not a positive: %+v disputed=%d incidents=%d", s2, rep.Disputed, rep.Incidents)
	}
	if !s1.Insufficient {
		t.Error("fewer than 20 positives is reported as insufficient")
	}
	// agreement 17/20, chance 0.56 -> (0.85-0.56)/0.44
	if len(rep.Kappa) != 1 || math.Abs(rep.Kappa[0].Kappa-0.659090909) > 1e-6 || rep.DoubleLabeledShare != 1 {
		t.Errorf("kappa %+v share %v", rep.Kappa, rep.DoubleLabeledShare)
	}
}

func TestLoadLabels(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "kim"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "kim", "s.jsonl"), []byte(`{"session":"s","meta":{"goal":"x","success":"yes","reviewed":true}}
{"session":"s","start":1,"end":3,"symptom":"S2"}
`), 0o644)
	ls, err := LoadLabels(dir)
	if err != nil || len(ls) != 2 || ls[1].Labeler != "kim" || ls[0].Meta == nil {
		t.Fatalf("labels %+v %v", ls, err)
	}
	_ = os.WriteFile(filepath.Join(dir, "kim", "bad.jsonl"), []byte("{oops\n"), 0o644)
	if _, err := LoadLabels(dir); err == nil {
		t.Error("broken label files are errors")
	}
}

func TestEvaluateScoresRulesOnConsensus(t *testing.T) {
	var evs []*event.Event
	for i := int64(0); i < 30; i++ {
		evs = append(evs, &event.Event{Seq: i})
	}
	data := map[string]SessionData{"s": {Events: evs, Verdicts: []detect.Signal{
		{Detector: "S1", Rule: "s1.identical_rerun", Seq: 5, Evidence: []int64{5}, Confidence: 0.95},  // justified incident
		{Detector: "S1", Rule: "s1.verify_ratio", Seq: 12, Evidence: []int64{12}, Confidence: 0.9},    // incident not worth stopping
		{Detector: "S1", Rule: "s1.identical_rerun", Seq: 21, Evidence: []int64{21}, Confidence: 0.9}, // inside a reviewed clean span
		{Detector: "S2", Rule: "s2.stuck_error", Seq: 26, Evidence: []int64{26}, Confidence: 0.9},     // disputed span: not scored
	}}}
	labels := []Label{
		{Session: "s", Start: 4, End: 6, Symptom: "S1", Justified: "yes", Labeler: "a"},
		{Session: "s", Start: 5, End: 7, Symptom: "S1", Justified: "yes", Labeler: "b"},
		{Session: "s", Start: 11, End: 13, Symptom: "S1", Justified: "no", Labeler: "a"},
		{Session: "s", Start: 11, End: 13, Symptom: "S1", Justified: "no", Labeler: "b"},
		{Session: "s", Start: 20, End: 22, Symptom: "none", Labeler: "a"},
		{Session: "s", Start: 25, End: 27, Symptom: "S2", Labeler: "b"},
	}
	// adding a third labeler who agrees must not create new incidents
	more := append(append([]Label(nil), labels...), Label{Session: "s", Start: 4, End: 6, Symptom: "S1", Justified: "yes", Labeler: "c"},
		Label{Session: "s", Start: 11, End: 13, Symptom: "S1", Justified: "no", Labeler: "c"})
	for _, ls := range [][]Label{labels, more} {
		rep := Evaluate("holdout", []string{"s"}, data, ls)
		if rep.Incidents != 2 {
			t.Fatalf("incidents %d, want 2 regardless of labeler count", rep.Incidents)
		}
		got := map[string]RuleScore{}
		for _, r := range rep.Rules {
			got[r.Rule] = r
		}
		ir := got["s1.identical_rerun"]
		if ir.Detections != 2 || ir.Correct != 1 || ir.Justified != 1 || ir.NormalHits != 1 {
			t.Fatalf("identical_rerun %+v", ir)
		}
		if vr := got["s1.verify_ratio"]; vr.Correct != 1 || vr.Justified != 0 {
			t.Fatalf("verify_ratio hit an incident not worth stopping: %+v", vr)
		}
		if _, ok := got["s2.stuck_error"]; ok {
			t.Fatal("a detection on a disputed span is neither right nor wrong")
		}
		if !ir.Insufficient {
			t.Fatal("two detections are an insufficient sample")
		}
	}
}
