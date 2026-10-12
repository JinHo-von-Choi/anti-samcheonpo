package eval

import (
	"encoding/json"
	"errors"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
	"io"
	"os"
	"time"
)

type ReviewItem struct {
	Session               string `json:"session"`
	Agent                 string `json:"agent"`
	Start, End            int64
	Stratum               string `json:"sampling_stratum"`
	SourceDigest          string `json:"source_digest"`
	RepositoryHash        string `json:"repository_hash"`
	NativeSourceAvailable bool   `json:"native_source_available"`
	NativeSourceSHA256    string `json:"native_source_sha256,omitempty"`
	HasAppliedPatch       bool   `json:"has_applied_patch"`
	// These are requests for review, not labels or ground truth.
	LabelStatus string `json:"label_status"`
}
type ReviewQueue struct {
	Version             string         `json:"version"`
	CreatedAt           time.Time      `json:"created_at"`
	RequestedCandidates int            `json:"requested_candidates"`
	RequestedControls   int            `json:"requested_controls"`
	Candidates          int            `json:"candidate_intervals"`
	Controls            int            `json:"unreviewed_control_intervals"`
	Platforms           map[string]int `json:"platforms"`
	Items               []ReviewItem   `json:"items"`
	LabelsCreated       bool           `json:"labels_created"`
	PromotionEligible   bool           `json:"promotion_eligible"`
	Limitation          string         `json:"limitation"`
	Digest              string         `json:"digest"`
}

func ReviewSourceDigest(events []*event.Event) string { return rulepack.Digest(events) }
func ReviewQueueDigest(q ReviewQueue) string          { q.Digest = ""; return rulepack.Digest(q) }
func LoadReviewQueue(path string) (ReviewQueue, error) {
	var q ReviewQueue
	f, err := os.Open(path)
	if err != nil {
		return q, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 32<<20))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&q); err != nil {
		return q, err
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		return q, errors.New("review queue must contain one JSON object")
	}
	if q.Version != "review-queue/2" || q.Digest != ReviewQueueDigest(q) || q.LabelsCreated || q.PromotionEligible || len(q.Items) > 20000 {
		return q, errors.New("invalid frozen review queue")
	}
	seen := map[string]bool{}
	c, n := 0, 0
	for _, x := range q.Items {
		if x.Session == "" || seen[x.Session] || x.Start < 0 || x.End < x.Start || x.SourceDigest == "" {
			return q, errors.New("invalid or duplicate review interval")
		}
		seen[x.Session] = true
		switch x.Stratum {
		case "candidate":
			c++
		case "unreviewed_control":
			n++
		default:
			return q, errors.New("invalid sampling stratum")
		}
	}
	if c != q.Candidates || n != q.Controls {
		return q, errors.New("review sample counts do not match")
	}
	return q, nil
}
