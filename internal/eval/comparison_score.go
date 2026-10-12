package eval

import "sort"

// ComparisonCase describes one frozen review interval. Prediction values are
// positive, negative or unknown; missing observations never imply negative.
type ComparisonCase struct {
	Consensus   string
	Reason      string
	Predictions map[string]string
}

type ComparisonScore struct {
	Detector           string    `json:"detector"`
	TP                 int       `json:"true_positive"`
	FP                 int       `json:"false_positive"`
	TN                 int       `json:"true_negative"`
	FN                 int       `json:"false_negative"`
	UnknownPredictions int       `json:"unknown_predictions"`
	Precision          *float64  `json:"precision"`
	PrecisionCI        *Interval `json:"precision_ci"`
	Recall             *float64  `json:"recall"`
	RecallCI           *Interval `json:"recall_ci"`
}

type ComparisonScores struct {
	Unit             string            `json:"unit"`
	Intervals        int               `json:"intervals"`
	CommonEvaluated  int               `json:"common_evaluated_intervals"`
	UnknownReviews   map[string]int    `json:"unknown_review_reasons"`
	PredictionGaps   int               `json:"reviewed_intervals_with_prediction_gaps"`
	Scores           []ComparisonScore `json:"scores"`
	ReleaseValidated bool              `json:"release_validated"`
}

// ScoreComparison uses the same fully observed, agreed intervals for every
// detector. These are descriptive scores on a stratified sample, not proof
// of population accuracy, human independence, or intervention effectiveness.
func ScoreComparison(cases []ComparisonCase, detectors []string) ComparisonScores {
	r := ComparisonScores{Unit: "frozen_review_interval", Intervals: len(cases), UnknownReviews: map[string]int{}}
	names := append([]string(nil), detectors...)
	sort.Strings(names)
	for _, name := range names {
		r.Scores = append(r.Scores, ComparisonScore{Detector: name})
	}
	known := func(v string) bool { return v == "positive" || v == "negative" }
	for _, c := range cases {
		complete := len(names) > 0
		for i, name := range names {
			if !known(c.Predictions[name]) {
				r.Scores[i].UnknownPredictions++
				complete = false
			}
		}
		if !known(c.Consensus) {
			reason := c.Reason
			if reason == "" {
				reason = "review_unknown"
			}
			r.UnknownReviews[reason]++
			continue
		}
		if !complete {
			r.PredictionGaps++
			continue
		}
		r.CommonEvaluated++
		for i, name := range names {
			s := &r.Scores[i]
			switch {
			case c.Consensus == "positive" && c.Predictions[name] == "positive":
				s.TP++
			case c.Consensus == "negative" && c.Predictions[name] == "positive":
				s.FP++
			case c.Consensus == "negative":
				s.TN++
			default:
				s.FN++
			}
		}
	}
	for i := range r.Scores {
		s := &r.Scores[i]
		if n := s.TP + s.FP; n > 0 {
			p, ci := float64(s.TP)/float64(n), Wilson(s.TP, n)
			s.Precision, s.PrecisionCI = &p, &ci
		}
		if n := s.TP + s.FN; n > 0 {
			p, ci := float64(s.TP)/float64(n), Wilson(s.TP, n)
			s.Recall, s.RecallCI = &p, &ci
		}
	}
	return r
}
