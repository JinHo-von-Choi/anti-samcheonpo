// Package ledger stores sessions, events, verdicts and seals in SQLite.
package ledger

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/procgroup"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/seal"
)

//go:embed migrations/*.sql
var migrations embed.FS

// DB wraps the ledger database.
type DB struct{ *sql.DB }

// Open opens (creating if needed) the ledger and applies migrations.
func Open(path string) (*DB, error) {
	if err := procgroup.MkdirAllPrivate(filepath.Dir(path)); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", fileURI(path)+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db}, nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var cur int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&cur); err != nil {
		return err
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	if len(names) > 0 {
		latest, err := strconv.Atoi(strings.SplitN(filepath.Base(names[len(names)-1]), "_", 2)[0])
		if err != nil {
			return err
		}
		if cur > latest {
			return fmt.Errorf("unsupported ledger schema version: %d", cur)
		}
	}
	for _, n := range names {
		v, err := strconv.Atoi(strings.SplitN(filepath.Base(n), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration name %s: %w", n, err)
		}
		if v <= cur {
			continue
		}
		b, err := migrations.ReadFile(n)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(b)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", n, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, v); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Version returns the applied schema version.
func (d *DB) Version() int {
	var v int
	_ = d.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&v)
	return v
}

func ts(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// NeedsAudit reports whether a source file changed since it was last analyzed.
func (d *DB) NeedsAudit(path string, size, mtime int64) bool {
	var s, m int64
	var ver string
	err := d.QueryRow(`SELECT size, mtime, eval_version FROM audit_source WHERE path=?`, path).Scan(&s, &m, &ver)
	return err != nil || s != size || m != mtime || ver != analyze.EvaluatorVersion
}

// BuildSeal computes the seal and chained rows for a result.
func BuildSeal(r *analyze.Result) (seal.Seal, []seal.Row) {
	rows := seal.Build(r.Session.Events, r.Verdicts)
	s := seal.Seal{
		Spec: seal.SpecVersion, SessionID: r.Session.ID, Agent: r.Session.Agent, SourceHash: r.Session.SourceHash,
		ContractHash: r.ContractHash, Evaluator: analyze.EvaluatorVersion, PriceVersion: r.PriceVersion, ConfigHash: r.ConfigHash,
		Head: seal.Head(rows), Rows: len(rows), TotalMicro: r.Totals.Micro, TotalTokens: r.Totals.Tokens, BucketMicro: r.Totals.BucketMicro,
	}
	return s, rows
}

// SaveAnalysis replaces a session's rows with the analysis result.
func (d *DB) SaveAnalysis(r *analyze.Result, size, mtime int64, msgs func(detect.Signal) (string, string)) (seal.Seal, error) {
	s := r.Session
	sl, rows := BuildSeal(r)
	tx, err := d.Begin()
	if err != nil {
		return sl, err
	}
	defer tx.Rollback()
	var existingAgent string
	err = tx.QueryRow(`SELECT agent FROM session WHERE id=?`, s.ID).Scan(&existingAgent)
	if err != nil && err != sql.ErrNoRows {
		return sl, err
	}
	if err == nil && existingAgent != s.Agent {
		return sl, fmt.Errorf("session ID belongs to another agent")
	}
	for _, t := range []string{"event", "verdict"} {
		if _, err := tx.Exec(`DELETE FROM `+t+` WHERE session_id=?`, s.ID); err != nil {
			return sl, err
		}
	}
	if _, err := tx.Exec(`DELETE FROM session WHERE id=?`, s.ID); err != nil {
		return sl, err
	}
	var q, w int64
	q = r.Totals.SymptomTotal()
	w = r.Totals.SymptomTokenTotal()
	summary := firstLine(s.FirstPrompt, 80)
	if _, err := tx.Exec(`INSERT INTO session(id,agent,model,project_path,contract_hash,started_at,ended_at,mode,source_path,source_hash,
		total_micro_krw,total_tokens,unpriced_tokens,waste_micro_krw,waste_tokens,grade,format_ok,quota_used_pct,quota_window_min,first_prompt_summary,analyzed_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.ID, s.Agent, s.Model, s.ProjectPath, r.ContractHash, ts(s.StartedAt), ts(s.EndedAt), s.Mode, s.SourcePath, s.SourceHash,
		r.Totals.Micro, r.Totals.Tokens, r.Totals.UnpricedTokens, q, w, r.Grade, b2i(r.FormatOK), nullF(s.QuotaUsedPct), nullI(s.QuotaWindowMin), summary,
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		return sl, err
	}
	stmt, err := tx.Prepare(`INSERT INTO event(session_id,seq,ts,kind,tool,raw_tool,cmd_fp,paths,ws_before,ws_after,result_fp,exit_code,
		tokens_in,tokens_out,tokens_cache_read,tokens_cache_write,model,cost_micro_krw,priced,category,category_basis,bucket,symptom,estimated,forced,parent,summary,source_ref,chain_hash)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return sl, err
	}
	defer stmt.Close()
	for i, ev := range s.Events {
		paths, _ := json.Marshal(ev.Paths)
		var exit any
		if ev.ExitCode != nil {
			exit = *ev.ExitCode
		}
		if _, err := stmt.Exec(s.ID, ev.Seq, ts(ev.TS), string(ev.Kind), ev.Tool, ev.RawTool, ev.CmdFP, string(paths), ev.WSBefore, ev.WSAfter,
			ev.ResultFP, exit, ev.Usage.In, ev.Usage.Out, ev.Usage.CacheRead, ev.Usage.CacheWrite, ev.Usage.Model, ev.CostMicroKRW, b2i(ev.Priced),
			string(ev.Category), ev.Basis, ev.Bucket, ev.Symptom, b2i(ev.Estimated), b2i(ev.Forced), b2i(ev.Parent), ev.Summary, ev.SourceRef, rows[i].Chain); err != nil {
			return sl, err
		}
	}
	vs, err := tx.Prepare(`INSERT INTO verdict(id,session_id,seq,detector,rule,confidence,evidence_event_ids,waste_micro_krw,level,is_primary,suppressed,estimate,arm,facts,message_user,message_agent,chain_hash)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return sl, err
	}
	defer vs.Close()
	for i, v := range r.Verdicts {
		ev, _ := json.Marshal(v.Evidence)
		facts, _ := json.Marshal(v.Facts)
		var mu, ma string
		if msgs != nil {
			mu, ma = msgs(v)
		}
		if _, err := vs.Exec(v.ID, s.ID, v.Seq, v.Detector, v.Rule, v.Confidence, string(ev), v.WasteMicro, int(v.Level), b2i(v.Primary),
			b2i(v.Suppressed), b2i(v.Estimate), v.Arm, string(facts), mu, ma, rows[len(s.Events)+i].Chain); err != nil {
			return sl, err
		}
	}
	sj, _ := json.Marshal(sl)
	if _, err := tx.Exec(`INSERT OR REPLACE INTO session_seal(session_id, seal) VALUES (?,?)`, s.ID, string(sj)); err != nil {
		return sl, err
	}
	if s.SourcePath != "" {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO audit_source(path,session_id,agent,size,mtime,eval_version) VALUES (?,?,?,?,?,?)`,
			s.SourcePath, s.ID, s.Agent, size, mtime, analyze.EvaluatorVersion); err != nil {
			return sl, err
		}
	}
	last := time.Time{}
	for _, ev := range s.Events {
		if ev.TS.After(last) {
			last = ev.TS
		}
	}
	if last.IsZero() {
		last = s.StartedAt
	}
	reason, closed := s.ObservationCloseReason, s.ObservationClosedAt
	if reason == "" {
		reason = "audit_snapshot"
		closed = time.Now()
	}
	if _, err := tx.Exec(`INSERT INTO session_observation(session_id,agent,last_activity_at,closed_at,close_reason,evaluator_version) VALUES(?,?,?,?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET last_activity_at=excluded.last_activity_at,closed_at=excluded.closed_at,close_reason=excluded.close_reason,evaluator_version=excluded.evaluator_version WHERE session_observation.agent=excluded.agent`, s.ID, s.Agent, ts(last), ts(closed), reason, analyze.EvaluatorVersion); err != nil {
		return sl, err
	}
	return sl, tx.Commit()
}

func nullF(f float64) any {
	if f == 0 {
		return nil
	}
	return f
}

func nullI(i int) any {
	if i == 0 {
		return nil
	}
	return i
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// SessionRow is a summary row for listing.
type SessionRow struct {
	ID, Agent, Model, Project, Grade, Prompt, Source, Mode string
	Analyzed                                               time.Time
	Started, Ended                                         time.Time
	Micro, Tokens, Unpriced, WasteMicro, WasteTokens       int64
	FormatOK                                               bool
	QuotaPct                                               float64
}

// Sessions lists analyzed sessions started after since.
func (d *DB) Sessions(since time.Time, agent, project string) ([]SessionRow, error) {
	q := `SELECT id,agent,COALESCE(model,''),COALESCE(project_path,''),COALESCE(grade,''),COALESCE(first_prompt_summary,''),COALESCE(source_path,''),
		COALESCE(started_at,''),COALESCE(ended_at,''),total_micro_krw,total_tokens,unpriced_tokens,waste_micro_krw,waste_tokens,format_ok,COALESCE(quota_used_pct,0),mode,COALESCE(analyzed_at,'')
		FROM session WHERE (started_at IS NULL OR started_at >= ?)`
	args := []any{since.UTC().Format(time.RFC3339Nano)}
	if agent != "" && agent != "all" {
		q += ` AND agent=?`
		args = append(args, agent)
	}
	if project != "" {
		q += ` AND project_path=?`
		args = append(args, project)
	}
	q += ` ORDER BY started_at`
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRow
	for rows.Next() {
		var r SessionRow
		var st, en, at string
		var fok int
		if err := rows.Scan(&r.ID, &r.Agent, &r.Model, &r.Project, &r.Grade, &r.Prompt, &r.Source, &st, &en, &r.Micro, &r.Tokens, &r.Unpriced,
			&r.WasteMicro, &r.WasteTokens, &fok, &r.QuotaPct, &r.Mode, &at); err != nil {
			return nil, err
		}
		r.Started, _ = time.Parse(time.RFC3339Nano, st)
		r.Ended, _ = time.Parse(time.RFC3339Nano, en)
		r.Analyzed, _ = time.Parse(time.RFC3339Nano, at)
		r.FormatOK = fok == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// Seal returns a stored session seal.
func (d *DB) Seal(id string) (seal.Seal, error) {
	var s string
	var sl seal.Seal
	if err := d.QueryRow(`SELECT seal FROM session_seal WHERE session_id=?`, id).Scan(&s); err != nil {
		return sl, err
	}
	return sl, json.Unmarshal([]byte(s), &sl)
}

// SourceOf returns the source path and agent of a session (prefix match on id).
func (d *DB) SourceOf(id string) (string, string, string, error) {
	var full, src, agent string
	err := d.QueryRow(`SELECT id, COALESCE(source_path,''), agent FROM session WHERE id=? OR id LIKE ? ORDER BY id LIMIT 1`, id, id+"%").Scan(&full, &src, &agent)
	return full, src, agent, err
}

// AddFeedback records user feedback on a verdict.
func (d *DB) AddFeedback(verdictID, session, rule, label, reason, note, project string) error {
	_, err := d.Exec(`INSERT INTO feedback(verdict_id,session_id,rule,label,reason,note,project_path,created_at) VALUES (?,?,?,?,?,?,?,?)`,
		verdictID, session, rule, label, reason, note, project, time.Now().UTC().Format(time.RFC3339))
	return err
}

// FalsePositives counts false-positive feedback for a rule in a project since t.
func (d *DB) FalsePositives(project, rule string, since time.Time) int {
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM feedback WHERE project_path=? AND rule=? AND label='false_positive' AND created_at>=?`,
		project, rule, since.UTC().Format(time.RFC3339)).Scan(&n)
	return n
}

// LastFalsePositive returns the time of the latest false positive for a rule.
func (d *DB) LastFalsePositive(project, rule string) time.Time {
	var s string
	_ = d.QueryRow(`SELECT COALESCE(MAX(created_at),'') FROM feedback WHERE project_path=? AND rule=? AND label='false_positive'`, project, rule).Scan(&s)
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// RecordIntervention stores a proposal. Queueing is not proof of delivery.
func (d *DB) RecordIntervention(v detect.Signal, session, channel string) error {
	_, err := d.Exec(`INSERT INTO intervention(verdict_id,session_id,seq,channel,level,arm,delivery_state) VALUES (?,?,?,?,?,?,?)`,
		v.ID, session, v.Seq, channel, int(v.Level), v.Arm, "proposed")
	return err
}

// EventsOf loads a session's ledger events (for labeling and eval).
func (d *DB) EventsOf(id string) ([]*event.Event, error) {
	rows, err := d.Query(`SELECT seq,COALESCE(ts,''),kind,COALESCE(tool,''),COALESCE(raw_tool,''),COALESCE(paths,'[]'),exit_code,cost_micro_krw,
		tokens_in,tokens_out,tokens_cache_read,tokens_cache_write,COALESCE(model,''),COALESCE(category,''),COALESCE(bucket,''),COALESCE(symptom,''),
		COALESCE(summary,''),COALESCE(source_ref,''),priced,COALESCE(cmd_fp,''),COALESCE(ws_before,''),COALESCE(ws_after,''),COALESCE(result_fp,''),forced
		FROM event WHERE session_id=? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*event.Event
	for rows.Next() {
		ev := &event.Event{SessionID: id}
		var tss, paths, kind, cat string
		var exit sql.NullInt64
		var priced, forced int
		if err := rows.Scan(&ev.Seq, &tss, &kind, &ev.Tool, &ev.RawTool, &paths, &exit, &ev.CostMicroKRW, &ev.Usage.In, &ev.Usage.Out,
			&ev.Usage.CacheRead, &ev.Usage.CacheWrite, &ev.Usage.Model, &cat, &ev.Bucket, &ev.Symptom, &ev.Summary, &ev.SourceRef, &priced,
			&ev.CmdFP, &ev.WSBefore, &ev.WSAfter, &ev.ResultFP, &forced); err != nil {
			return nil, err
		}
		ev.Forced = forced == 1
		ev.TS, _ = time.Parse(time.RFC3339Nano, tss)
		ev.Kind = event.Kind(kind)
		ev.Category = event.Category(cat)
		ev.Priced = priced == 1
		_ = json.Unmarshal([]byte(paths), &ev.Paths)
		if exit.Valid {
			x := int(exit.Int64)
			ev.ExitCode = &x
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// VerdictsOf loads a session's verdicts.
func (d *DB) VerdictsOf(id string) ([]detect.Signal, error) {
	rows, err := d.Query(`SELECT id,seq,detector,rule,confidence,evidence_event_ids,waste_micro_krw,level,is_primary,suppressed,estimate,COALESCE(arm,''),COALESCE(facts,'{}')
		FROM verdict WHERE session_id=? ORDER BY rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []detect.Signal
	for rows.Next() {
		var v detect.Signal
		var ev, facts string
		var lvl, pr, sup, est int
		if err := rows.Scan(&v.ID, &v.Seq, &v.Detector, &v.Rule, &v.Confidence, &ev, &v.WasteMicro, &lvl, &pr, &sup, &est, &v.Arm, &facts); err != nil {
			return nil, err
		}
		v.Level = detect.Level(lvl)
		v.Primary, v.Suppressed, v.Estimate = pr == 1, sup == 1, est == 1
		_ = json.Unmarshal([]byte(ev), &v.Evidence)
		_ = json.Unmarshal([]byte(facts), &v.Facts)
		out = append(out, v)
	}
	return out, rows.Err()
}

// BucketTotals rebuilds per-session totals from stored events and verdicts.
func (d *DB) BucketTotals(ids []string) map[string]analyze.Totals {
	out := map[string]analyze.Totals{}
	get := func(id string) analyze.Totals {
		t, ok := out[id]
		if !ok {
			t = analyze.Totals{BucketMicro: map[string]int64{}, BucketTokens: map[string]int64{}, SymptomMicro: map[string]int64{},
				SymptomTokens: map[string]int64{}, SymptomCount: map[string]int{}, Interventions: map[string]int{}}
		}
		return t
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
		out[id] = get(id)
	}
	rows, err := d.Query(`SELECT session_id, COALESCE(bucket,''), COALESCE(symptom,''), estimated, priced, SUM(cost_micro_krw),
		SUM(tokens_in+tokens_out+tokens_cache_read+tokens_cache_write), COUNT(*) FROM event GROUP BY session_id, bucket, symptom, estimated, priced`)
	if err != nil {
		return out
	}
	for rows.Next() {
		var id, b, s string
		var est, priced int
		var m, tok int64
		var n int
		if rows.Scan(&id, &b, &s, &est, &priced, &m, &tok, &n) != nil || !want[id] {
			continue
		}
		t := get(id)
		t.Micro += m
		t.Tokens += tok
		if priced == 0 {
			t.UnpricedTokens += tok
		}
		t.BucketMicro[b] += m
		t.BucketTokens[b] += tok
		if b == analyze.BucketWaste {
			t.SymptomMicro[s] += m
			t.SymptomTokens[s] += tok
			t.SymptomCount[s] += n
		}
		if est == 1 {
			t.EstimatedMicro += m
			t.EstimatedTokens += tok
		}
		out[id] = t
	}
	rows.Close()
	vr, err := d.Query(`SELECT session_id, level, COUNT(*) FROM verdict WHERE is_primary=1 AND level>0 GROUP BY session_id, level`)
	if err == nil {
		for vr.Next() {
			var id string
			var lvl, n int
			if vr.Scan(&id, &lvl, &n) == nil && want[id] {
				t := get(id)
				t.Interventions[fmt.Sprintf("L%d", lvl)] += n
				out[id] = t
			}
		}
		vr.Close()
	}
	sr, err := d.Query(`SELECT id, COALESCE(started_at,''), COALESCE(ended_at,'') FROM session`)
	if err == nil {
		for sr.Next() {
			var id, a, b string
			if sr.Scan(&id, &a, &b) == nil && want[id] {
				ta, _ := time.Parse(time.RFC3339Nano, a)
				tb, _ := time.Parse(time.RFC3339Nano, b)
				if !ta.IsZero() && tb.After(ta) {
					t := get(id)
					t.Minutes = int(tb.Sub(ta).Minutes())
					out[id] = t
				}
			}
		}
		sr.Close()
	}
	return out
}

// UpsertLive creates or refreshes a live session row.
func (d *DB) UpsertLive(id, agent, root, transcript, model string) error {
	res, err := d.Exec(`INSERT INTO session(id,agent,model,project_path,mode,source_path,started_at,analyzed_at) VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET model=COALESCE(NULLIF(excluded.model,''),session.model), mode='live',ended_at=NULL WHERE session.agent=excluded.agent`,
		id, agent, model, root, "live", transcript, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("session ID belongs to another agent")
	}
	return d.startObservation(id, agent)
}

func (d *DB) CheckSessionAgent(id, agent string) error {
	var old string
	err := d.QueryRow(`SELECT agent FROM session WHERE id=?`, id).Scan(&old)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if old != agent {
		return fmt.Errorf("session ID belongs to another agent")
	}
	return nil
}

// InsertLiveEvent writes one live event (replaced by SaveAnalysis at SessionEnd).
func (d *DB) InsertLiveEvent(id string, ev *event.Event) error {
	paths, _ := json.Marshal(ev.Paths)
	var exit any
	if ev.ExitCode != nil {
		exit = *ev.ExitCode
	}
	_, err := d.Exec(`INSERT OR REPLACE INTO event(session_id,seq,ts,kind,tool,raw_tool,cmd_fp,paths,ws_before,ws_after,result_fp,exit_code,
		tokens_in,tokens_out,tokens_cache_read,tokens_cache_write,model,cost_micro_krw,priced,category,category_basis,forced,summary,source_ref)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, ev.Seq, ts(ev.TS), string(ev.Kind), ev.Tool, ev.RawTool, ev.CmdFP, string(paths), ev.WSBefore, ev.WSAfter, ev.ResultFP, exit,
		ev.Usage.In, ev.Usage.Out, ev.Usage.CacheRead, ev.Usage.CacheWrite, ev.Usage.Model, ev.CostMicroKRW, b2i(ev.Priced), string(ev.Category), "live", b2i(ev.Forced), ev.Summary, ev.SourceRef)
	return err
}

// UpdateLiveCost updates the usage and cost of a live event.
func (d *DB) UpdateLiveCost(id string, ev *event.Event) error {
	_, err := d.Exec(`UPDATE event SET tokens_in=?,tokens_out=?,tokens_cache_read=?,tokens_cache_write=?,model=?,cost_micro_krw=?,priced=? WHERE session_id=? AND seq=?`,
		ev.Usage.In, ev.Usage.Out, ev.Usage.CacheRead, ev.Usage.CacheWrite, ev.Usage.Model, ev.CostMicroKRW, b2i(ev.Priced), id, ev.Seq)
	return err
}

// InsertLiveVerdict writes one live verdict.
func (d *DB) InsertLiveVerdict(id string, v detect.Signal, userMsg, agentMsg string) error {
	evd, _ := json.Marshal(v.Evidence)
	facts, _ := json.Marshal(v.Facts)
	_, err := d.Exec(`INSERT OR REPLACE INTO verdict(id,session_id,seq,detector,rule,confidence,evidence_event_ids,waste_micro_krw,level,is_primary,suppressed,estimate,arm,facts,message_user,message_agent)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ID, id, v.Seq, v.Detector, v.Rule, v.Confidence, string(evd), v.WasteMicro, int(v.Level), b2i(v.Primary), b2i(v.Suppressed), b2i(v.Estimate), v.Arm, string(facts), userMsg, agentMsg)
	return err
}

// InsertProgress records a checkpoint result.
func (d *DB) InsertProgress(id string, seq int64, met, total int, detail any) error {
	b, _ := json.Marshal(detail)
	_, err := d.Exec(`INSERT INTO progress(session_id,event_seq,ts,criteria_met,criteria_total,detail) VALUES (?,?,?,?,?,?)`,
		id, seq, time.Now().UTC().Format(time.RFC3339Nano), met, total, string(b))
	return err
}

// RecordContract records a contract state transition (confirmed: false stays visible).
func (d *DB) RecordContract(id, checksHash, state string) error {
	_, err := d.Exec(`INSERT INTO contract_record(session_id,checks_hash,state,recorded_at) VALUES (?,?,?,?)`, id, checksHash, state, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// Mode returns the stored mode of a session ("" when absent).
func (d *DB) Mode(id string) string {
	var m string
	_ = d.QueryRow(`SELECT mode FROM session WHERE id=?`, id).Scan(&m)
	return m
}

// fileURI turns a filesystem path into the SQLite file: URI form. The path is
// percent-escaped, since #, ? and % would otherwise end or alter the path, and
// a Windows drive path takes the three-slash form so that the drive letter is
// not read as a URI authority.
func fileURI(path string) string {
	slashed := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(slashed, "//") {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}
