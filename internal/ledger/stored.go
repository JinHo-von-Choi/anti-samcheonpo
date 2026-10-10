package ledger

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/seal"
)

// receiptSnapshot keeps the completed result, not a new analysis of mutable
// live rows. Events contain only the existing ledger fields and canonical
// seal inputs; transient command, prompt and output text are excluded.
type receiptSnapshot struct {
	Result analyze.Result `json:"result"`
	Events []*event.Event `json:"events"`
}

func encodeSnapshot(r *analyze.Result) ([]byte, error) {
	snap := receiptSnapshot{Result: *r}
	for _, ev := range r.Session.Events {
		snap.Events = append(snap.Events, &event.Event{
			SessionID: ev.SessionID, Seq: ev.Seq, TS: ev.TS, Kind: ev.Kind,
			Tool: ev.Tool, RawTool: ev.RawTool, CmdFP: ev.CmdFP, Paths: ev.Paths,
			WriteHashes: ev.WriteHashes, WSBefore: ev.WSBefore, WSAfter: ev.WSAfter,
			ExitCode: ev.ExitCode, ErrFPs: ev.ErrFPs, FailedTests: ev.FailedTests,
			ResultFP: ev.ResultFP, Usage: ev.Usage, CostMicroKRW: ev.CostMicroKRW,
			Priced: ev.Priced, Category: ev.Category, Bucket: ev.Bucket,
			Symptom: ev.Symptom, Estimated: ev.Estimated, Forced: ev.Forced,
			Parent: ev.Parent, Basis: ev.Basis, Summary: ev.Summary, SourceRef: ev.SourceRef,
		})
	}
	return json.Marshal(snap)
}

func (d *DB) snapshot(full string) (*analyze.Result, seal.Seal, error) {
	var payload, sealed, state string
	// Read the snapshot and its seal in one SQLite read snapshot. A session
	// reopened by the live writer must finish another analysis before issuance.
	err := d.QueryRow(`SELECT r.payload, s.seal, COALESCE(o.close_reason,'')
		FROM receipt_snapshot r LEFT JOIN session_seal s ON s.session_id=r.session_id
		LEFT JOIN session_observation o ON o.session_id=r.session_id
		WHERE r.session_id=?`, full).Scan(&payload, &sealed, &state)
	if err != nil {
		return nil, seal.Seal{}, err
	}
	if state == "open" {
		return nil, seal.Seal{}, fmt.Errorf("세션 %s는 관측 중이다. 분석 완료 후 영수증을 발급한다", full)
	}
	var snap receiptSnapshot
	var sl seal.Seal
	if err := json.Unmarshal([]byte(payload), &snap); err != nil {
		return nil, sl, fmt.Errorf("세션 %s 저장 분석 읽기: %w", full, err)
	}
	if err := json.Unmarshal([]byte(sealed), &sl); err != nil {
		return nil, sl, err
	}
	r := &snap.Result
	if r.Session == nil || r.Session.ID != full || sl.Spec != seal.SpecVersion {
		return nil, sl, fmt.Errorf("세션 %s 저장 분석 형식이 유효하지 않다", full)
	}
	for _, ev := range snap.Events {
		if ev == nil {
			return nil, sl, fmt.Errorf("세션 %s 저장 이벤트가 유효하지 않다", full)
		}
	}
	r.Session.Events = snap.Events
	got, _ := BuildSeal(r)
	// Stored verification checks the recorded evaluator, not the installed one.
	got.Evaluator = sl.Evaluator
	if !reflect.DeepEqual(got, sl) {
		return nil, sl, fmt.Errorf("세션 %s 저장 분석과 봉인이 일치하지 않는다", full)
	}
	if err := analyze.CheckInvariant(r.Session, r.Totals); err != nil {
		return nil, sl, fmt.Errorf("세션 %s 저장 합계 검증: %w", full, err)
	}
	return r, sl, nil
}

// StoredResult returns a verified completed analysis snapshot when available.
// Legacy rows can supply a partial receipt, but no verified seal.
func (d *DB) StoredResult(id string) (*analyze.Result, seal.Seal, error) {
	full, _, _, err := d.SourceOf(id)
	if err != nil {
		return nil, seal.Seal{}, fmt.Errorf("세션 %s를 원장에서 찾지 못했다: %w", id, err)
	}
	if r, sl, err := d.snapshot(full); err != sql.ErrNoRows {
		return r, sl, err
	}
	// Older ledgers cannot reconstruct all canonical fields or distinguish
	// explicitly observed zero usage. Do not present their old seal as verified.
	var agent, model, project, contractHash, startedS, endedS, mode, srcPath, srcHash, grade, first string
	var formatOK int
	var quotaPct sql.NullFloat64
	var quotaWin sql.NullInt64
	err = d.QueryRow(`SELECT agent, COALESCE(model,''), COALESCE(project_path,''), COALESCE(contract_hash,''),
		COALESCE(started_at,''), COALESCE(ended_at,''), mode, COALESCE(source_path,''), COALESCE(source_hash,''),
		COALESCE(grade,''), format_ok, quota_used_pct, quota_window_min, COALESCE(first_prompt_summary,'')
		FROM session WHERE id=?`, full).
		Scan(&agent, &model, &project, &contractHash, &startedS, &endedS, &mode, &srcPath, &srcHash,
			&grade, &formatOK, &quotaPct, &quotaWin, &first)
	if err != nil {
		return nil, seal.Seal{}, fmt.Errorf("세션 %s 기록 읽기: %w", full, err)
	}
	sl, err := d.Seal(full)
	if err != nil {
		return nil, seal.Seal{}, fmt.Errorf("세션 %s 봉인 읽기: %w", full, err)
	}
	events, err := d.EventsOf(full)
	if err != nil {
		return nil, seal.Seal{}, fmt.Errorf("세션 %s 이벤트 읽기: %w", full, err)
	}
	verdicts, err := d.VerdictsOf(full)
	if err != nil {
		return nil, seal.Seal{}, fmt.Errorf("세션 %s 판정 읽기: %w", full, err)
	}
	var started, ended time.Time
	if startedS != "" {
		if started, err = time.Parse(time.RFC3339Nano, startedS); err != nil {
			return nil, seal.Seal{}, fmt.Errorf("세션 %s 시작 시각 읽기: %w", full, err)
		}
	}
	if endedS != "" {
		if ended, err = time.Parse(time.RFC3339Nano, endedS); err != nil {
			return nil, seal.Seal{}, fmt.Errorf("세션 %s 종료 시각 읽기: %w", full, err)
		}
	}
	sess := &event.Session{
		ID: full, Agent: agent, Model: model, ProjectPath: project,
		StartedAt: started, EndedAt: ended, Mode: mode, SourcePath: srcPath, SourceHash: srcHash,
		FirstPrompt: first, Events: events,
		QuotaUsedPct: quotaPct.Float64, QuotaKnown: quotaPct.Valid, QuotaWindowMin: int(quotaWin.Int64),
	}
	r := &analyze.Result{
		Session: sess, Verdicts: verdicts, Totals: d.BucketTotals([]string{full})[full],
		Grade: grade, FormatOK: formatOK == 1, ContractHash: contractHash,
		ConfigHash: sl.ConfigHash, PriceVersion: sl.PriceVersion,
	}
	if r.Totals.Micro != sl.TotalMicro || r.Totals.Tokens != sl.TotalTokens || !reflect.DeepEqual(r.Totals.BucketMicro, sl.BucketMicro) {
		return nil, seal.Seal{}, fmt.Errorf("세션 %s 저장 합계와 봉인이 일치하지 않는다", full)
	}
	return r, seal.Seal{}, nil
}
