// Package eval computes detector precision/recall against human labels,
// Wilson confidence intervals, inter-labeler agreement and intervention
// experiment statistics.
package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// Label is one labeled interval (or session meta when Meta is set).
type Label struct {
	Session string `json:"session"`
	Start   int64  `json:"start"`
	End     int64  `json:"end"`
	Symptom string `json:"symptom"` // S1..S8, or "none" for a reviewed clean span
	Note    string `json:"note,omitempty"`
	// Justified and Outcome are the other two label axes: whether an
	// intervention at this span would have been right (yes | no | unsure), and
	// what was confirmed afterwards (resolved | unresolved | unknown). Empty
	// means the span was labeled for occurrence only.
	Justified string `json:"justified,omitempty"`
	Outcome   string `json:"outcome,omitempty"`
	Labeler   string `json:"labeler,omitempty"`
	Meta      *Meta  `json:"meta,omitempty"`
}

// Meta is session-level labeling information.
type Meta struct {
	Goal     string `json:"goal"`
	Success  string `json:"success"` // yes | no | partial | unknown
	Reviewed bool   `json:"reviewed"`
}

// Split assigns sessions to calibration and holdout sets.
type Split struct {
	Version     string                `json:"version,omitempty"`
	Purpose     string                `json:"purpose,omitempty"`
	Calibration []string              `json:"calibration"`
	Holdout     []string              `json:"holdout"`
	Groups      map[string]SplitGroup `json:"groups,omitempty"`
}

type SplitGroup struct {
	Repository string `json:"repository"`
	Family     string `json:"family"`
}

// LoadSplit reads and validates a split file.
func LoadSplit(p string) (Split, error) {
	var s Split
	b, err := os.ReadFile(p)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("%s: %w", p, err)
	}
	if err := s.Validate(); err != nil {
		return s, err
	}
	return s, nil
}

func (s Split) Validate() error {
	if s.Version != "" && s.Version != "split/2" {
		return fmt.Errorf("unsupported split version")
	}
	if s.Version == "split/2" && (s.Purpose != "public_regression" && s.Purpose != "preregistered_holdout" || len(s.Calibration) == 0 || len(s.Holdout) == 0) {
		return fmt.Errorf("v2 split requires purpose and both partitions")
	}
	seen := map[string]bool{}
	owners := map[string]int{}
	for partition, ids := range [][]string{s.Calibration, s.Holdout} {
		for _, id := range ids {
			if id == "" || seen[id] {
				return fmt.Errorf("중복 또는 빈 세션: %s", id)
			}
			seen[id] = true
			group, ok := s.Groups[id]
			if s.Version == "split/2" && (!ok || group.Repository == "" || group.Family == "") {
				return fmt.Errorf("세션 %s의 저장소/과제 계열 누락", id)
			}
			for prefix, value := range map[string]string{"repo:": group.Repository, "family:": group.Family} {
				if value != "" {
					key := prefix + value
					if previous, ok := owners[key]; ok && previous != partition {
						return fmt.Errorf("보정/보류 간 저장소 또는 과제 계열 누출: %s", key)
					}
					owners[key] = partition
				}
			}
		}
	}
	for id := range s.Groups {
		if !seen[id] {
			return fmt.Errorf("분할되지 않은 세션 그룹: %s", id)
		}
	}
	return nil
}

// LoadLabels reads labels/<labeler>/*.jsonl under dir.
func LoadLabels(dir string) ([]Label, error) {
	var out []Label
	labelers, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, l := range labelers {
		if !l.IsDir() {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(dir, l.Name(), "*.jsonl"))
		sort.Strings(files)
		for _, f := range files {
			fh, err := os.Open(f)
			if err != nil {
				return nil, err
			}
			sc := bufio.NewScanner(fh)
			sc.Buffer(make([]byte, 1<<20), 1<<20)
			n := 0
			for sc.Scan() {
				n++
				line := strings.TrimSpace(sc.Text())
				if line == "" {
					continue
				}
				var lb Label
				if err := json.Unmarshal([]byte(line), &lb); err != nil {
					fh.Close()
					return nil, fmt.Errorf("%s:%d: %w", f, n, err)
				}
				lb.Labeler = l.Name()
				out = append(out, lb)
			}
			fh.Close()
		}
	}
	return out, nil
}

// Interval is a Wilson score interval.
type Interval struct{ Lo, Hi float64 }

// Wilson returns the 95% Wilson interval for k successes in n trials.
func Wilson(k, n int) Interval {
	if n == 0 {
		return Interval{0, 1}
	}
	z := 1.959963984540054
	p := float64(k) / float64(n)
	nn := float64(n)
	den := 1 + z*z/nn
	c := (p + z*z/(2*nn)) / den
	h := z * math.Sqrt(p*(1-p)/nn+z*z/(4*nn*nn)) / den
	return Interval{math.Max(0, c-h), math.Min(1, c+h)}
}

// SymptomScore is the evaluation of one symptom.
type SymptomScore struct {
	Symptom          string   `json:"symptom"`
	TP, FP           int      `json:"-"`
	Detections       int      `json:"detections"`
	Correct          int      `json:"correct"`
	Positives        int      `json:"positives"`
	Covered          int      `json:"covered"`
	Precision        float64  `json:"precision"`
	PrecisionCI      Interval `json:"precision_ci"`
	Recall           float64  `json:"recall"`
	RecallCI         Interval `json:"recall_ci"`
	LatencyMedianKRW int64    `json:"latency_median_krw"`
	Insufficient     bool     `json:"insufficient"`
}

// Report is the result of an evaluation.
type Report struct {
	Set                string         `json:"set"`
	Sessions           int            `json:"sessions"`
	Symptoms           []SymptomScore `json:"symptoms"`
	Overall            SymptomScore   `json:"overall"`
	Deterministic      SymptomScore   `json:"deterministic"`
	Kappa              []KappaPair    `json:"kappa"`
	DoubleLabeledShare float64        `json:"double_labeled_share"`
}

// KappaPair is Cohen's kappa between two labelers.
type KappaPair struct {
	A      string  `json:"a"`
	B      string  `json:"b"`
	Kappa  float64 `json:"kappa"`
	Events int     `json:"events"`
}

// SessionData holds what evaluation needs for one session.
type SessionData struct {
	Events   []*event.Event
	Verdicts []detect.Signal
}

// MinPositives is the per-symptom sample floor.
const MinPositives = 20

var deterministic = map[string]bool{"S1": true, "S2": true, "S4": true, "S5": true, "S7": true, "S8": true}

// Evaluate scores verdicts against labels for the given sessions.
func Evaluate(set string, sessions []string, data map[string]SessionData, labels []Label) Report {
	inSet := map[string]bool{}
	for _, s := range sessions {
		inSet[s] = true
	}
	// labels by session and symptom (union across labelers)
	type iv struct {
		s, e int64
		sym  string
	}
	lab := map[string][]iv{}
	for _, l := range labels {
		if !inSet[l.Session] || l.Meta != nil || l.Symptom == "" || l.Symptom == "none" {
			continue
		}
		lab[l.Session] = append(lab[l.Session], iv{l.Start, l.End, l.Symptom})
	}
	scores := map[string]*SymptomScore{}
	get := func(s string) *SymptomScore {
		if scores[s] == nil {
			scores[s] = &SymptomScore{Symptom: s}
		}
		return scores[s]
	}
	latency := map[string][]int64{}
	for _, sid := range sessions {
		d := data[sid]
		cost := map[int64]int64{}
		var order []int64
		for _, ev := range d.Events {
			cost[ev.Seq] = ev.CostMicroKRW
			order = append(order, ev.Seq)
		}
		covered := map[int]int64{} // label index -> first detection seq
		for _, v := range d.Verdicts {
			if v.Suppressed || v.Confidence < 0.5 {
				continue
			}
			sc := get(v.Detector)
			sc.Detections++
			hit := false
			for i, l := range lab[sid] {
				if l.sym != v.Detector {
					continue
				}
				for _, e := range v.Evidence {
					if e >= l.s && e <= l.e {
						hit = true
						if f, ok := covered[i]; !ok || v.Seq < f {
							covered[i] = v.Seq
						}
						break
					}
				}
			}
			if hit {
				sc.Correct++
			}
		}
		for i, l := range lab[sid] {
			sc := get(l.sym)
			sc.Positives++
			if f, ok := covered[i]; ok {
				sc.Covered++
				var c int64
				for _, sq := range order {
					if sq >= l.s && sq <= f {
						c += cost[sq]
					}
				}
				latency[l.sym] = append(latency[l.sym], c)
			}
		}
	}
	rep := Report{Set: set, Sessions: len(sessions)}
	var keys []string
	for k := range scores {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	all, det := &SymptomScore{Symptom: "all"}, &SymptomScore{Symptom: "deterministic"}
	for _, k := range keys {
		s := scores[k]
		finish(s, latency[k])
		rep.Symptoms = append(rep.Symptoms, *s)
		for _, agg := range []*SymptomScore{all, det} {
			if agg == det && !deterministic[k] {
				continue
			}
			agg.Detections += s.Detections
			agg.Correct += s.Correct
			agg.Positives += s.Positives
			agg.Covered += s.Covered
		}
	}
	finish(all, nil)
	finish(det, nil)
	rep.Overall, rep.Deterministic = *all, *det
	rep.Kappa, rep.DoubleLabeledShare = kappa(sessions, data, labels)
	return rep
}

func finish(s *SymptomScore, lat []int64) {
	if s.Detections > 0 {
		s.Precision = float64(s.Correct) / float64(s.Detections)
	}
	s.PrecisionCI = Wilson(s.Correct, s.Detections)
	if s.Positives > 0 {
		s.Recall = float64(s.Covered) / float64(s.Positives)
	}
	s.RecallCI = Wilson(s.Covered, s.Positives)
	s.Insufficient = s.Positives < MinPositives
	if len(lat) > 0 {
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		s.LatencyMedianKRW = lat[len(lat)/2] / 1_000_000
	}
}

// kappa computes event-level Cohen's kappa for every labeler pair that
// labeled the same session.
func kappa(sessions []string, data map[string]SessionData, labels []Label) ([]KappaPair, float64) {
	byLab := map[string]map[string][]Label{} // session -> labeler -> labels
	for _, l := range labels {
		if l.Meta != nil {
			continue
		}
		if byLab[l.Session] == nil {
			byLab[l.Session] = map[string][]Label{}
		}
		byLab[l.Session][l.Labeler] = append(byLab[l.Session][l.Labeler], l)
	}
	type pairKey struct{ a, b string }
	agree := map[pairKey][][2]string{}
	double := 0
	labeled := 0
	for _, sid := range sessions {
		m := byLab[sid]
		if len(m) == 0 {
			continue
		}
		labeled++
		if len(m) < 2 {
			continue
		}
		double++
		var names []string
		for n := range m {
			names = append(names, n)
		}
		sort.Strings(names)
		for i := 0; i < len(names); i++ {
			for j := i + 1; j < len(names); j++ {
				k := pairKey{names[i], names[j]}
				for _, ev := range data[sid].Events {
					agree[k] = append(agree[k], [2]string{symAt(m[names[i]], ev.Seq), symAt(m[names[j]], ev.Seq)})
				}
			}
		}
	}
	var out []KappaPair
	for k, pairs := range agree {
		out = append(out, KappaPair{A: k.a, B: k.b, Kappa: cohen(pairs), Events: len(pairs)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].A+out[i].B < out[j].A+out[j].B })
	share := 0.0
	if labeled > 0 {
		share = float64(double) / float64(labeled)
	}
	return out, share
}

func symAt(ls []Label, seq int64) string {
	for _, l := range ls {
		if seq >= l.Start && seq <= l.End && l.Symptom != "" {
			return l.Symptom
		}
	}
	return "none"
}

// Cohen computes Cohen's kappa over paired categorical ratings.
func cohen(pairs [][2]string) float64 {
	n := float64(len(pairs))
	if n == 0 {
		return 0
	}
	agree := 0.0
	ca, cb := map[string]float64{}, map[string]float64{}
	for _, p := range pairs {
		if p[0] == p[1] {
			agree++
		}
		ca[p[0]]++
		cb[p[1]]++
	}
	po := agree / n
	pe := 0.0
	for k, v := range ca {
		pe += (v / n) * (cb[k] / n)
	}
	if pe == 1 {
		return 1
	}
	return (po - pe) / (1 - pe)
}

// Cohen is exported for tests.
func Cohen(pairs [][2]string) float64 { return cohen(pairs) }
