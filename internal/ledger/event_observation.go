package ledger

import (
	"encoding/json"
	"regexp"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

type observationPayload struct {
	Execution          *event.Execution            `json:"execution,omitempty"`
	ActionFP           string                      `json:"action_fp,omitempty"`
	ResearchLinks      []event.ResearchLink        `json:"research_links,omitempty"`
	ResearchSources    []event.ResearchSource      `json:"research_sources,omitempty"`
	ResultObserved     bool                        `json:"result_observed,omitempty"`
	RequirementChanges []event.RequirementChange   `json:"requirement_changes,omitempty"`
	PromptOrigin       string                      `json:"prompt_origin,omitempty"`
	ObservationFP      string                      `json:"observation_fp,omitempty"`
	Selection          string                      `json:"selection,omitempty"`
	Requirements       []event.RequirementProgress `json:"requirements,omitempty"`
	IsError            bool                        `json:"is_error,omitempty"`
	Unknown            bool                        `json:"unknown,omitempty"`
	Mutating           bool                        `json:"mutating,omitempty"`
	Estimated          bool                        `json:"estimated,omitempty"`
	DurationMS         int64                       `json:"duration_ms,omitempty"`
	TimeoutMS          int64                       `json:"timeout_ms,omitempty"`
	Decisions          []event.Decision            `json:"decisions,omitempty"`
	Progress           event.WorkProgress          `json:"progress,omitempty"`
	Context            string                      `json:"context,omitempty"`
	Target             string                      `json:"target,omitempty"`
	Result             string                      `json:"result,omitempty"`
	ExecFP             string                      `json:"exec_fp,omitempty"`
	ExecCertain        bool                        `json:"exec_certain,omitempty"`
	Background         bool                        `json:"background,omitempty"`
	OriginSeq          int64                       `json:"origin_seq,omitempty"`
	ErrFPs             []string                    `json:"err_fps,omitempty"`
	FailedTests        []string                    `json:"failed_tests,omitempty"`
	Reliability        *event.Reliability          `json:"reliability,omitempty"`
}

var safeIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,63}$`)

func observationJSON(ev *event.Event) ([]byte, error) {
	p := observationPayload{PromptOrigin: ev.PromptOrigin, ObservationFP: ev.ObservationFP, Selection: ev.BehaviorSelection, Requirements: ev.Requirements, IsError: ev.IsError, Unknown: ev.Unknown, Mutating: ev.Mutating, Estimated: ev.Estimated, DurationMS: ev.DurationMS, TimeoutMS: ev.TimeoutMS, Progress: ev.WorkProgress, Context: ev.BehaviorContext, Target: ev.BehaviorTarget, Result: ev.BehaviorResult, ExecFP: ev.ExecFP, ExecCertain: ev.ExecCertain, Background: ev.Background, OriginSeq: ev.OriginSeq, ErrFPs: ev.ErrFPs, FailedTests: ev.FailedTests, Reliability: ev.Reliability}
	p.RequirementChanges = ev.RequirementChanges
	p.ResultObserved = ev.ResultObserved
	p.Execution = ev.Execution
	p.ResearchSources = ev.ResearchSources
	p.ActionFP = ev.ActionFP
	p.ResearchLinks = ev.ResearchLinks
	for _, d := range ev.Decisions {
		if safeIdentifier.MatchString(d.Rule) && safeIdentifier.MatchString(d.Code) {
			p.Decisions = append(p.Decisions, d)
		}
	}
	return json.Marshal(p)
}

func (d *DB) SaveEventObservation(id string, ev *event.Event) error {
	b, err := observationJSON(ev)
	if err != nil {
		return err
	}
	_, err = d.Exec(`INSERT INTO event_observation(session_id,seq,payload) VALUES(?,?,?) ON CONFLICT(session_id,seq) DO UPDATE SET payload=excluded.payload`, id, ev.Seq, string(b))
	return err
}

func (d *DB) loadEventObservations(id string, evs []*event.Event) error {
	var exists int
	if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='event_observation'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	rows, err := d.Query(`SELECT seq,payload FROM event_observation WHERE session_id=? ORDER BY seq`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	by := map[int64]*event.Event{}
	for _, ev := range evs {
		by[ev.Seq] = ev
	}
	for rows.Next() {
		var seq int64
		var raw string
		if err = rows.Scan(&seq, &raw); err != nil {
			return err
		}
		var p observationPayload
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return err
		}
		ev := by[seq]
		if ev == nil {
			continue
		}
		applyObservation(ev, p)
	}
	return rows.Err()
}

// OpenReadOnly performs diagnostics without creating or migrating a ledger.
func OpenReadOnly(path string) (*DB, error) {
	db, err := openReadOnly(path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &DB{db}, nil
}

func applyObservation(ev *event.Event, p observationPayload) {
	ev.RequirementChanges = p.RequirementChanges
	ev.ResultObserved = p.ResultObserved
	ev.Execution = p.Execution
	ev.ResearchSources = p.ResearchSources
	ev.ActionFP = p.ActionFP
	ev.ResearchLinks = p.ResearchLinks
	ev.PromptOrigin = p.PromptOrigin
	ev.ObservationFP = p.ObservationFP
	ev.Requirements = p.Requirements
	ev.IsError, ev.Unknown, ev.Mutating, ev.Estimated = p.IsError, p.Unknown, p.Mutating, p.Estimated
	ev.DurationMS, ev.TimeoutMS = p.DurationMS, p.TimeoutMS
	ev.Decisions = p.Decisions
	ev.WorkProgress = p.Progress
	ev.BehaviorSelection = p.Selection
	ev.BehaviorContext = p.Context
	ev.BehaviorTarget = p.Target
	ev.BehaviorResult = p.Result
	ev.ExecFP = p.ExecFP
	ev.ExecCertain = p.ExecCertain
	ev.Background = p.Background
	ev.OriginSeq = p.OriginSeq
	ev.ErrFPs = p.ErrFPs
	ev.FailedTests = p.FailedTests
	ev.Reliability = p.Reliability
}
