package eval

import "testing"

func TestComparisonScoresKeepUnknownOutOfSharedDenominator(t *testing.T) {
	cases := []ComparisonCase{
		{Consensus: "positive", Predictions: map[string]string{"a": "positive", "b": "negative"}},
		{Consensus: "negative", Predictions: map[string]string{"a": "positive", "b": "negative"}},
		{Consensus: "positive", Predictions: map[string]string{"a": "negative", "b": "positive"}},
		{Consensus: "negative", Predictions: map[string]string{"a": "negative", "b": "positive"}},
		{Consensus: "positive", Predictions: map[string]string{"a": "positive", "b": "unknown"}},
		{Consensus: "unknown", Reason: "reviewer_disagreement", Predictions: map[string]string{"a": "positive", "b": "positive"}},
	}
	r := ScoreComparison(cases, []string{"b", "a"})
	if r.CommonEvaluated != 4 || r.PredictionGaps != 1 || r.UnknownReviews["reviewer_disagreement"] != 1 || r.ReleaseValidated {
		t.Fatal(r)
	}
	for _, s := range r.Scores {
		if s.TP != 1 || s.FP != 1 || s.FN != 1 || s.TN != 1 || s.Precision == nil || *s.Precision != .5 || s.Recall == nil || *s.Recall != .5 {
			t.Fatal(s)
		}
		if s.PrecisionCI.Lo > .5 || s.PrecisionCI.Hi < .5 {
			t.Fatal("invalid Wilson interval", s)
		}
	}
}

func TestComparisonScoresWithoutTruthHaveNullMetrics(t *testing.T) {
	r := ScoreComparison([]ComparisonCase{{Consensus: "unknown", Predictions: map[string]string{"a": "negative"}}}, []string{"a"})
	s := r.Scores[0]
	if r.CommonEvaluated != 0 || s.Precision != nil || s.Recall != nil || s.PrecisionCI != nil || s.RecallCI != nil {
		t.Fatal("missing labels presented as zero accuracy", r)
	}
}
