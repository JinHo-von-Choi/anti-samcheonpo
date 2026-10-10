package eval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/policy"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
)

const CorpusVersion = "reliability-corpus/1"

// CorpusManifest fixes source joins, cohort and group splits before labels are
// loaded. AgentTime groups use benchmark + task_raw_id; id joins the run.
type CorpusRollout struct {
	Experiment  bool     `json:"experiment_enabled"`
	Mode        string   `json:"mode"`
	ShadowRules []string `json:"shadow_rules"`
}
type CorpusManifest struct {
	EvaluationRollout *CorpusRollout `json:"evaluation_rollout,omitempty"`
	MetadataPath      string         `json:"metadata_path,omitempty"`
	MetadataSHA256    string         `json:"metadata_sha256,omitempty"`
	Filters           string         `json:"filters,omitempty"`
	Version           string         `json:"version"`
	Cohort            string         `json:"cohort"`
	Runs              []CorpusRun    `json:"runs"`
}
type CorpusRun struct {
	SessionID      string `json:"session_id,omitempty"`
	ID             string `json:"id"`
	Benchmark      string `json:"benchmark"`
	TaskRawID      string `json:"task_raw_id"`
	Split          string `json:"split"`
	Path           string `json:"path"`
	Format         string `json:"format"` // claude | codex | normalized
	SHA256         string `json:"sha256"`
	ContractPath   string `json:"contract_path,omitempty"`
	ContractSHA256 string `json:"contract_sha256,omitempty"`
	ContextPath    string `json:"context_path,omitempty"`
	ContextSHA256  string `json:"context_sha256,omitempty"`
	Provenance     string `json:"provenance,omitempty"`
}

// Context is independently prepared, keyed to original event sequences. It
// contains no verdict labels. Its provenance and hash must be preregistered.
type CorpusContext struct {
	Events map[int64]*event.Reliability `json:"events"`
}
type CorpusLabels struct {
	Version string        `json:"version"`
	Labeler string        `json:"labeler"`
	Labels  []CorpusLabel `json:"labels"`
}
type CorpusLabel struct {
	RunID    string `json:"run_id"`
	Rule     string `json:"rule"`
	Positive bool   `json:"positive"`
	RawWait  *bool  `json:"raw_wait,omitempty"`
}
type CorpusMetrics struct {
	TP          int      `json:"tp"`
	FP          int      `json:"fp"`
	FN          int      `json:"fn"`
	TN          int      `json:"tn"`
	Precision   *float64 `json:"precision"`
	Recall      *float64 `json:"recall"`
	FPR         *float64 `json:"false_positive_rate"`
	PrecisionCI Interval `json:"precision_ci"`
	RecallCI    Interval `json:"recall_ci"`
	FPRCI       Interval `json:"fpr_ci"`
}
type CorpusRuleReport struct {
	Ineligible int           `json:"ineligible_runs"`
	Rule       string        `json:"rule"`
	Eligible   int           `json:"eligible_runs"`
	Unknown    int           `json:"unknown_runs"`
	Labeled    int           `json:"labeled_runs"`
	Predicted  int           `json:"detected_runs"`
	Delivered  int           `json:"deliverable_runs"`
	Overall    CorpusMetrics `json:"overall"`
	Applicable CorpusMetrics `json:"applicable"`
}
type CorpusRunReport struct {
	DeliveryArms  map[string]string   `json:"delivery_arms,omitempty"`
	Applicability map[string]string   `json:"applicability"`
	Evicted       uint64              `json:"evicted_candidates"`
	Deferred      uint64              `json:"deferred_observations"`
	Reasons       map[string][]string `json:"ineligibility_reasons,omitempty"`
	ID            string              `json:"id"`
	Group         string              `json:"group"`
	SourceSHA256  string              `json:"source_sha256"`
	Events        int                 `json:"events"`
	Complete      int                 `json:"complete_observations"`
	Unknown       int                 `json:"unknown_observations"`
	RawWaits      int                 `json:"raw_waits"`
	FormatOK      bool                `json:"format_ok"`
	Excluded      string              `json:"excluded,omitempty"`
	Rules         map[string]bool     `json:"detected"`
	Eligible      map[string]bool     `json:"eligible"`
	Delivered     map[string]bool     `json:"deliverable"`
	Verdicts      []detect.Signal     `json:"verdicts,omitempty"`
}
type CorpusReport struct {
	EvaluatorVersion       string              `json:"evaluator_version"`
	ConfigSHA256           string              `json:"config_sha256"`
	PriceVersion           string              `json:"price_version"`
	EvaluationRollout      CorpusRollout       `json:"evaluation_rollout"`
	SourceRuns             int                 `json:"metadata_source_runs"`
	ManifestRuns           int                 `json:"manifest_runs"`
	Filters                string              `json:"filters,omitempty"`
	Version                string              `json:"version"`
	Cohort                 string              `json:"cohort"`
	Split                  string              `json:"split"`
	ManifestSHA256         string              `json:"manifest_sha256"`
	LabelsSHA256           string              `json:"labels_sha256,omitempty"`
	Labeler                string              `json:"labeler,omitempty"`
	Runs                   []CorpusRunReport   `json:"runs"`
	Rules                  []CorpusRuleReport  `json:"rules"`
	RawWaiting             CorpusMetrics       `json:"raw_waiting"`
	Excluded               int                 `json:"excluded_runs"`
	ParseCoverage          *float64            `json:"parse_coverage"`
	EligibilityCoverage    map[string]*float64 `json:"eligibility_coverage"`
	EffectivenessValidated bool                `json:"effectiveness_validated"`
}

func corpusDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func corpusJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}
func corpusRead(base, path, expected string) ([]byte, error) {
	if path == "" || len(expected) != 64 {
		return nil, fmt.Errorf("path and SHA256 are required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if corpusDigest(b) != expected {
		return nil, fmt.Errorf("source hash mismatch: %s", path)
	}
	return b, nil
}
func corpusFraction(k, n int) *float64 {
	if n == 0 {
		return nil
	}
	v := float64(k) / float64(n)
	return &v
}
func (m *CorpusMetrics) add(predicted, positive bool) {
	switch {
	case predicted && positive:
		m.TP++
	case predicted:
		m.FP++
	case positive:
		m.FN++
	default:
		m.TN++
	}
}
func (m *CorpusMetrics) finish() {
	m.Precision = corpusFraction(m.TP, m.TP+m.FP)
	m.Recall = corpusFraction(m.TP, m.TP+m.FN)
	m.FPR = corpusFraction(m.FP, m.FP+m.TN)
	m.PrecisionCI = Wilson(m.TP, m.TP+m.FP)
	m.RecallCI = Wilson(m.TP, m.TP+m.FN)
	m.FPRCI = Wilson(m.FP, m.FP+m.TN)
}

// EvaluateCorpus is observational replay: it never runs recorded commands,
// opens a user ledger, reads user config, or changes model/auth settings.
// Scratch roots also prevent detectors from reading the original workspace.
func EvaluateCorpus(manifestPath, labelsPath, set string) (CorpusReport, error) {
	rep := CorpusReport{EvaluatorVersion: analyze.EvaluatorVersion, Version: CorpusVersion, Split: set, EligibilityCoverage: map[string]*float64{}}
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return rep, err
	}
	rep.ManifestSHA256 = corpusDigest(b)
	var manifest CorpusManifest
	if err = corpusJSON(b, &manifest); err != nil {
		return rep, err
	}
	if manifest.Version != CorpusVersion || manifest.Cohort == "" || len(manifest.Runs) == 0 || (set != "train" && set != "eval") {
		return rep, fmt.Errorf("invalid corpus version, cohort, runs or split")
	}
	rep.Cohort = manifest.Cohort
	rep.ManifestRuns = len(manifest.Runs)
	rep.Filters = manifest.Filters
	if strings.HasPrefix(strings.ToLower(manifest.Cohort), "agenttime") && (manifest.MetadataPath == "" || manifest.Filters == "") {
		return rep, fmt.Errorf("AgentTime requires pinned release metadata and explicit cohort filters")
	}
	if manifest.MetadataPath != "" {
		metadata, e := corpusRead(filepath.Dir(manifestPath), manifest.MetadataPath, manifest.MetadataSHA256)
		if e != nil {
			return rep, e
		}
		var release struct {
			Runs []struct {
				ID        string `json:"id"`
				Benchmark string `json:"benchmark"`
				TaskRawID string `json:"task_raw_id"`
			} `json:"runs"`
		}
		if e = json.Unmarshal(metadata, &release); e != nil {
			return rep, e
		}
		rep.SourceRuns = len(release.Runs)
		joins := map[string]string{}
		for _, r := range release.Runs {
			if _, ok := joins[r.ID]; ok {
				return rep, fmt.Errorf("duplicate release run ID")
			}
			joins[r.ID] = r.Benchmark + "\x00" + r.TaskRawID
		}
		for _, r := range manifest.Runs {
			if group, ok := joins[r.ID]; !ok || group != r.Benchmark+"\x00"+r.TaskRawID {
				return rep, fmt.Errorf("missing or inconsistent release join: %s", r.ID)
			}
		}
	}
	groups, ids := map[string]string{}, map[string]bool{}
	hashSplits := map[string]string{}
	for _, run := range manifest.Runs {
		if run.ID == "" || ids[run.ID] || run.Benchmark == "" || run.TaskRawID == "" || (run.Split != "train" && run.Split != "eval") {
			return rep, fmt.Errorf("missing run/group identity or duplicate run %s", run.ID)
		}
		ids[run.ID] = true
		if split := hashSplits[run.SHA256]; split != "" && split != run.Split {
			return rep, fmt.Errorf("source hash crosses train/eval")
		}
		hashSplits[run.SHA256] = run.Split
		group := run.Benchmark + "\x00" + run.TaskRawID
		if prev := groups[group]; prev != "" && prev != run.Split {
			return rep, fmt.Errorf("task group crosses train/eval: %s", run.ID)
		}
		groups[group] = run.Split
	}
	base := filepath.Dir(manifestPath)
	scratch, err := os.MkdirTemp("", "samcheonpo-corpus-")
	if err != nil {
		return rep, err
	}
	defer os.RemoveAll(scratch)
	prices, err := cost.Load("")
	if err != nil {
		return rep, err
	}
	cfg := config.Default()
	if profile := manifest.EvaluationRollout; profile != nil {
		if profile.Mode != "shadow" && profile.Mode != "recommend" && profile.Mode != "validated" {
			return rep, fmt.Errorf("invalid evaluation rollout mode")
		}
		cfg.Experiment.Enabled = profile.Experiment
		cfg.Rollout.Mode = profile.Mode
		cfg.Rollout.ShadowRules = profile.ShadowRules
	}
	rep.PriceVersion = prices.Version
	cfgBytes, err := json.Marshal(cfg)
	if err != nil {
		return rep, err
	}
	rep.ConfigSHA256 = corpusDigest(cfgBytes)
	rep.EvaluationRollout = CorpusRollout{Experiment: cfg.Experiment.Enabled, Mode: cfg.Rollout.Mode, ShadowRules: append([]string(nil), cfg.Rollout.ShadowRules...)}
	rules := []string{"s1.explicit_waiting", "s8.progress_stall"}
	for _, run := range manifest.Runs {
		if run.Split != set {
			continue
		}
		rr := CorpusRunReport{ID: run.ID, Group: run.Benchmark + ":" + run.TaskRawID, SourceSHA256: run.SHA256, Rules: map[string]bool{}, Eligible: map[string]bool{}, Delivered: map[string]bool{}}
		rr, err = replayCorpusRun(base, scratch, run, rr, prices, cfg)
		if err != nil {
			rr.Excluded = err.Error()
			rep.Excluded++
		}
		rep.Runs = append(rep.Runs, rr)
	}
	if len(rep.Runs) == 0 {
		return rep, fmt.Errorf("selected split is empty")
	}
	// Labels are read only after predictions are fixed. Missing labels are
	// unscored, never converted into negative examples.
	labels := CorpusLabels{}
	if labelsPath != "" {
		lb, e := os.ReadFile(labelsPath)
		if e != nil {
			return rep, e
		}
		rep.LabelsSHA256 = corpusDigest(lb)
		if e = corpusJSON(lb, &labels); e != nil {
			return rep, e
		}
		if labels.Version != CorpusVersion || labels.Labeler == "" {
			return rep, fmt.Errorf("label version and independent labeler are required")
		}
		rep.Labeler = labels.Labeler
	}
	annotations := map[string]CorpusLabel{}
	for _, label := range labels.Labels {
		if !ids[label.RunID] || !slices.Contains(rules, label.Rule) {
			return rep, fmt.Errorf("unknown label run or rule")
		}
		key := label.RunID + "\x00" + label.Rule
		if _, ok := annotations[key]; ok {
			return rep, fmt.Errorf("duplicate label")
		}
		annotations[key] = label
	}
	for _, rule := range rules {
		score := CorpusRuleReport{Rule: rule}
		for _, rr := range rep.Runs {
			if rr.Excluded != "" {
				continue
			}
			if rr.Eligible[rule] {
				score.Eligible++
			} else if rr.Applicability[rule] == "ineligible" {
				score.Ineligible++
			} else {
				score.Unknown++
			}
			if rr.Rules[rule] {
				score.Predicted++
			}
			if rr.Delivered[rule] {
				score.Delivered++
			}
			if label, ok := annotations[rr.ID+"\x00"+rule]; ok {
				score.Labeled++
				score.Overall.add(rr.Rules[rule], label.Positive)
				if rr.Eligible[rule] {
					score.Applicable.add(rr.Rules[rule], label.Positive)
				}
				if rule == "s1.explicit_waiting" && label.RawWait != nil {
					rep.RawWaiting.add(rr.RawWaits > 0, *label.RawWait)
				}
			}
		}
		score.Overall.finish()
		score.Applicable.finish()
		rep.Rules = append(rep.Rules, score)
		rep.EligibilityCoverage[rule] = corpusFraction(score.Eligible, len(rep.Runs)-rep.Excluded)
	}
	rep.RawWaiting.finish()
	rep.ParseCoverage = corpusFraction(len(rep.Runs)-rep.Excluded, len(rep.Runs))
	// Local fixtures or one report cannot satisfy the independent rollout gate.
	rep.EffectivenessValidated = false
	return rep, nil
}

func replayCorpusRun(base, scratch string, run CorpusRun, rr CorpusRunReport, prices *cost.Table, cfg config.Config) (CorpusRunReport, error) {
	b, err := corpusRead(base, run.Path, run.SHA256)
	if err != nil {
		return rr, err
	}
	var session *event.Session
	if run.Format == "normalized" {
		var evs []*event.Event
		if err = corpusJSON(b, &evs); err != nil {
			return rr, err
		}
		session = &event.Session{ID: run.ID, Agent: "normalized", Events: evs}
	} else if run.Format == "claude" || run.Format == "codex" {
		for line, record := range bytes.Split(b, []byte("\n")) {
			if len(bytes.TrimSpace(record)) > 0 && !json.Valid(record) {
				return rr, fmt.Errorf("invalid JSONL record %d", line+1)
			}
		}
		// Parse the verified bytes, without Claude's optional sibling-subagent merge.
		// Run IDs are metadata, never used as paths.
		source := filepath.Join(scratch, corpusDigest([]byte(run.ID))+".jsonl")
		if err = os.WriteFile(source, b, 0600); err != nil {
			return rr, err
		}
		session, err = sources.Parse(sources.File{Path: source, Agent: run.Format})
		if err != nil {
			return rr, err
		}
		expectedSession := run.SessionID
		if expectedSession == "" {
			expectedSession = run.ID
		}
		if session.ID != expectedSession {
			return rr, fmt.Errorf("transcript run ID does not match metadata join")
		}
	} else {
		return rr, fmt.Errorf("unsupported transcript format %q", run.Format)
	}
	if len(session.Events) == 0 {
		return rr, fmt.Errorf("no normalized events")
	}
	var c *contract.Contract
	if run.ContractPath != "" {
		if run.Provenance == "" {
			return rr, fmt.Errorf("context provenance is required")
		}
		raw, e := corpusRead(base, run.ContractPath, run.ContractSHA256)
		if e != nil {
			return rr, e
		}
		var errs []contract.ValidationError
		c, errs = contract.Parse(raw)
		if len(errs) > 0 {
			return rr, fmt.Errorf("invalid evaluation contract: %v", errs)
		}
	}
	if run.ContextPath != "" {
		if c == nil || run.Provenance == "" {
			return rr, fmt.Errorf("context needs fixed contract and provenance")
		}
		raw, e := corpusRead(base, run.ContextPath, run.ContextSHA256)
		if e != nil {
			return rr, e
		}
		var context CorpusContext
		if e = corpusJSON(raw, &context); e != nil {
			return rr, e
		}
		seen := map[int64]bool{}
		for _, ev := range session.Events {
			if ev == nil {
				return rr, fmt.Errorf("nil normalized event")
			}
			seen[ev.Seq] = true
			if r := context.Events[ev.Seq]; r != nil {
				ev.Reliability = r
			}
		}
		for seq := range context.Events {
			if !seen[seq] {
				return rr, fmt.Errorf("context references missing event %d", seq)
			}
		}
	}
	seqs := map[int64]bool{}
	for _, ev := range session.Events {
		if ev == nil || seqs[ev.Seq] {
			return rr, fmt.Errorf("nil or duplicate normalized event")
		}
		seqs[ev.Seq] = true
		if ev.SessionID == "" {
			ev.SessionID = session.ID
		}
		if ev.SessionID != session.ID {
			return rr, fmt.Errorf("event session differs from run")
		}
		if ev.Reliability != nil && c == nil {
			ev.Reliability = &event.Reliability{Wait: ev.Reliability.Wait, WaitKind: ev.Reliability.WaitKind, Phase: ev.Reliability.Phase}
		}
	}
	session.ProjectPath = scratch
	result, err := analyze.Run(session, analyze.Options{Config: cfg, Prices: prices, Contract: c, Accepted: c != nil})
	if err != nil {
		return rr, err
	}
	rr.Evicted, rr.Deferred = result.ReliabilityEvicted, result.ReliabilityDeferred
	rr.Reasons = map[string][]string{}
	rr.DeliveryArms = map[string]string{}
	rr.Applicability = map[string]string{"s1.explicit_waiting": "unknown", "s8.progress_stall": "unknown"}
	rr.Events = len(session.Events)
	rr.FormatOK = result.FormatOK
	if !rr.FormatOK {
		return rr, fmt.Errorf("adapter format/usage pairing incomplete")
	}
	for _, ev := range session.Events {
		r := ev.Reliability
		if r == nil || !r.Complete {
			rr.Unknown++
		} else {
			rr.Complete++
		}
		if r != nil && (r.Wait == "explicit" || r.WaitKind == "sleep" || r.WaitKind == "tool") && ev.ExitCode != nil && *ev.ExitCode == 0 && !ev.Background {
			rr.RawWaits++
		}
		if r != nil && c != nil && r.TaskID != "" && r.Revision > 0 && r.AuthorityHash == contract.AuthorityDigest(c) && r.Complete && !r.Flaky && !r.SourceMissing && (ev.CallID != "" || ev.SourceRef != "") {
			if c.TemporalRequirement != nil && !c.TemporalConflict(cfg.Detectors.S8.CeilingHours*3600) && r.CriteriaMet && c.WaitingObservationSatisfied(r.Observation) {
				rr.Eligible["s1.explicit_waiting"] = true
				rr.Applicability["s1.explicit_waiting"] = "eligible"
			}
			if !c.Explore && r.Observation != "pending" && detect.ExecutedVerify(ev) && r.CheckID != "" && r.InputHash != "" && r.CommandHash != "" && r.EnvironmentHash != "" {
				rr.Eligible["s8.progress_stall"] = true
				rr.Applicability["s8.progress_stall"] = "eligible"
			}
		}
	}
	for _, v := range result.Verdicts {
		if !policy.AdviceOnly(v.Rule) {
			continue
		}
		rr.Verdicts = append(rr.Verdicts, v)
		if reason, ok := v.Facts["eligibility"].(string); ok && reason != "eligible" && !slices.Contains(rr.Reasons[v.Rule], reason) {
			rr.Reasons[v.Rule] = append(rr.Reasons[v.Rule], reason)
		}
		if reason, ok := v.Facts["eligibility"].(string); ok && !rr.Eligible[v.Rule] && (reason == "legitimate_wait" || reason == "criteria_unconfirmed" || reason == "observation_pending") {
			rr.Applicability[v.Rule] = "ineligible"
		}
		if v.Level > detect.L0 {
			rr.Rules[v.Rule] = true
		}
		arm := v.Arm
		if arm == "" && !cfg.Experiment.Enabled {
			arm = "off"
		}
		if arm == "" && v.Level == detect.L1 && cfg.Experiment.Enabled {
			arm = detect.Arm(session.ID, v.Rule)
		}
		if arm != "" {
			rr.DeliveryArms[v.Rule] = arm
		}
		if v.Primary && !v.Suppressed && v.Level > detect.L0 && arm != "none" && policy.RolloutRoute(cfg.Rollout.Mode, v.Rule, cfg.Rollout.ValidatedRules) != "observe" && !slices.Contains(cfg.Rollout.ShadowRules, v.Rule) {
			rr.Delivered[v.Rule] = true
		}
	}
	return rr, nil
}
