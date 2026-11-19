package ledger

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// BasisUnlinked marks a replayed reproach with no ledger session.
const BasisUnlinked = "replay_unlinked"

// Reproach is a scolding user message and the request span before it.
type Reproach struct {
	SessionID string   `json:"session_id"`
	Seq       int64    `json:"seq"`
	StartSeq  int64    `json:"start_seq"`
	Agent     string   `json:"agent"`
	Project   string   `json:"project_path,omitempty"`
	Basis     string   `json:"basis"`
	Themes    []string `json:"themes"`
	TextHash  string   `json:"text_hash"`
	// Matched are the rules that fired in the span and match a theme.
	Matched []string `json:"matched"`
	// Missed are the themes no fired rule matches.
	Missed      []string  `json:"missed"`
	DoneClaimed bool      `json:"done_claimed"`
	SpanMicro   int64     `json:"span_micro_krw"`
	SpanTokens  int64     `json:"span_tokens"`
	ReworkMicro int64     `json:"rework_micro_krw"`
	ReworkTok   int64     `json:"rework_tokens"`
	CreatedAt   time.Time `json:"created_at"`
}

func jsonList(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// SaveReproach inserts or replaces one reproach row.
func (d *DB) SaveReproach(r Reproach) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	_, err := d.Exec(`INSERT INTO reproach(session_id,seq,start_seq,agent,project_path,basis,themes,text_hash,matched,missed,done_claimed,span_micro_krw,span_tokens,rework_micro_krw,rework_tokens,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(session_id,seq) DO UPDATE SET start_seq=excluded.start_seq,themes=excluded.themes,
		matched=excluded.matched,missed=excluded.missed,done_claimed=excluded.done_claimed,span_micro_krw=excluded.span_micro_krw,span_tokens=excluded.span_tokens,
		rework_micro_krw=excluded.rework_micro_krw,rework_tokens=excluded.rework_tokens`,
		r.SessionID, r.Seq, r.StartSeq, r.Agent, r.Project, r.Basis, jsonList(r.Themes), r.TextHash, jsonList(r.Matched), jsonList(r.Missed),
		b2i(r.DoneClaimed), r.SpanMicro, r.SpanTokens, r.ReworkMicro, r.ReworkTok, ts(r.CreatedAt))
	return err
}

// SaveReproachSession records how many user messages a session's reproaches
// are out of. observed is false when the session had no user input to read.
func (d *DB) SaveReproachSession(session, agent, basis string, userMessages int, observed bool, started time.Time) error {
	if started.IsZero() {
		started = time.Now()
	}
	_, err := d.Exec(`INSERT INTO reproach_session(session_id,agent,basis,user_messages,observed,started_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET user_messages=excluded.user_messages,observed=excluded.observed,basis=excluded.basis`,
		session, agent, basis, userMessages, b2i(observed), ts(started))
	return err
}

// SessionReproaches returns a session's reproaches in order.
func (d *DB) SessionReproaches(session string) ([]Reproach, error) {
	return d.reproaches(`WHERE session_id=? ORDER BY seq`, session)
}

func (d *DB) reproaches(where string, args ...any) ([]Reproach, error) {
	rows, err := d.Query(`SELECT session_id,seq,start_seq,agent,project_path,basis,themes,text_hash,matched,missed,done_claimed,span_micro_krw,span_tokens,rework_micro_krw,rework_tokens,created_at FROM reproach `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []Reproach
	for rows.Next() {
		var r Reproach
		var themes, matched, missed, created string
		var done int
		if err := rows.Scan(&r.SessionID, &r.Seq, &r.StartSeq, &r.Agent, &r.Project, &r.Basis, &themes, &r.TextHash, &matched, &missed, &done,
			&r.SpanMicro, &r.SpanTokens, &r.ReworkMicro, &r.ReworkTok, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(themes), &r.Themes)
		_ = json.Unmarshal([]byte(matched), &r.Matched)
		_ = json.Unmarshal([]byte(missed), &r.Missed)
		r.DoneClaimed = done == 1
		r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		res = append(res, r)
	}
	return res, rows.Err()
}

// ReproachWeek is one week of the reproach rate.
type ReproachWeek struct {
	Week         string `json:"week"`
	UserMessages int    `json:"user_messages"`
	Reproaches   int    `json:"reproaches"`
}

// ReproachReport sums reproaches over sessions started at or after Since.
type ReproachReport struct {
	Since time.Time `json:"since"`
	// Sessions with user input read; Unobserved had none to read.
	Sessions     int            `json:"sessions"`
	Unobserved   int            `json:"unobserved_sessions"`
	UserMessages int            `json:"user_messages"`
	Reproaches   int            `json:"reproaches"`
	DoneClaimed  int            `json:"done_claimed"`
	Themes       map[string]int `json:"themes"`
	// Matched counts, per rule, reproached spans whose theme the rule's
	// verdict matched; Missed counts, per theme, spans no rule matched.
	Matched     map[string]int `json:"matched"`
	Missed      map[string]int `json:"missed"`
	SpanMicro   int64          `json:"span_micro_krw"`
	SpanTokens  int64          `json:"span_tokens"`
	ReworkMicro int64          `json:"rework_micro_krw"`
	ReworkTok   int64          `json:"rework_tokens"`
	ByAgent     map[string]int `json:"by_agent"`
	// Unlinked are reproaches read from a transcript the ledger has no
	// session for: they count in the rate but match no rule.
	Unlinked int            `json:"unlinked"`
	Weeks    []ReproachWeek `json:"weeks"`
}

// Rate is reproaches per user message, or -1 when no message was read.
func (r *ReproachReport) Rate() float64 {
	if r.UserMessages == 0 {
		return -1
	}
	return float64(r.Reproaches) / float64(r.UserMessages)
}

// ReproachSummary sums the reproach rows of sessions started since.
func (d *DB) ReproachSummary(since time.Time) (*ReproachReport, error) {
	rep := &ReproachReport{Since: since, Themes: map[string]int{}, Matched: map[string]int{}, Missed: map[string]int{}, ByAgent: map[string]int{}}
	weeks := map[string]*ReproachWeek{}
	week := func(t time.Time) *ReproachWeek {
		y, w := t.ISOWeek()
		k := fmt.Sprintf("%d-W%02d", y, w)
		if weeks[k] == nil {
			weeks[k] = &ReproachWeek{Week: k}
		}
		return weeks[k]
	}
	rows, err := d.Query(`SELECT user_messages,observed,started_at FROM reproach_session WHERE started_at>=?`, ts(since))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n, observed int
		var started string
		if err := rows.Scan(&n, &observed, &started); err != nil {
			rows.Close()
			return nil, err
		}
		if observed == 0 {
			rep.Unobserved++
			continue
		}
		rep.Sessions++
		rep.UserMessages += n
		if t, err := time.Parse(time.RFC3339Nano, started); err == nil {
			week(t).UserMessages += n
		}
	}
	rows.Close()
	list, err := d.reproaches(`WHERE session_id IN (SELECT session_id FROM reproach_session WHERE started_at>=? AND observed=1)`, ts(since))
	if err != nil {
		return nil, err
	}
	// a reproach counts in the week its session started, as the messages
	// it is out of do
	started := map[string]time.Time{}
	srows, err := d.Query(`SELECT session_id,started_at FROM reproach_session WHERE started_at>=? AND observed=1`, ts(since))
	if err != nil {
		return nil, err
	}
	for srows.Next() {
		var id, at string
		if err := srows.Scan(&id, &at); err != nil {
			srows.Close()
			return nil, err
		}
		started[id], _ = time.Parse(time.RFC3339Nano, at)
	}
	srows.Close()
	for _, r := range list {
		rep.Reproaches++
		rep.ByAgent[r.Agent]++
		if r.Basis == BasisUnlinked {
			rep.Unlinked++
		}
		if r.DoneClaimed {
			rep.DoneClaimed++
		}
		for _, t := range r.Themes {
			rep.Themes[t]++
		}
		if len(r.Themes) == 0 {
			rep.Themes["unclassified"]++
		}
		for _, m := range r.Matched {
			rep.Matched[m]++
		}
		for _, m := range r.Missed {
			rep.Missed[m]++
		}
		rep.SpanMicro += r.SpanMicro
		rep.SpanTokens += r.SpanTokens
		rep.ReworkMicro += r.ReworkMicro
		rep.ReworkTok += r.ReworkTok
		week(started[r.SessionID]).Reproaches++
	}
	for _, w := range weeks {
		rep.Weeks = append(rep.Weeks, *w)
	}
	sort.Slice(rep.Weeks, func(i, j int) bool { return rep.Weeks[i].Week < rep.Weeks[j].Week })
	return rep, nil
}

// ReproachBasis returns how a session's reproaches were recorded ("live",
// "replay") or "" when they were not.
func (d *DB) ReproachBasis(session string) (string, error) {
	var basis string
	err := d.QueryRow(`SELECT basis FROM reproach_session WHERE session_id=?`, session).Scan(&basis)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return basis, err
}

// SessionStart returns a session's start time from the ledger, or false
// when the ledger has no such session.
func (d *DB) SessionStart(session string) (time.Time, bool, error) {
	var started string
	err := d.QueryRow(`SELECT COALESCE(started_at,'') FROM session WHERE id=?`, session).Scan(&started)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	t, _ := time.Parse(time.RFC3339Nano, started)
	return t, true, nil
}

// UnpricedModel is a model whose tokens ran without a price.
type UnpricedModel struct {
	Agent, Model string
	Sessions     int
	Tokens       int64
}

// UnpricedModels lists the models of sessions started since whose tokens
// had no price.
func (d *DB) UnpricedModels(since time.Time) ([]UnpricedModel, error) {
	rows, err := d.Query(`SELECT agent,COALESCE(model,''),COUNT(*),SUM(unpriced_tokens) FROM session WHERE started_at>=? AND unpriced_tokens>0 GROUP BY 1,2 ORDER BY 4 DESC`, ts(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnpricedModel
	for rows.Next() {
		var m UnpricedModel
		if err := rows.Scan(&m.Agent, &m.Model, &m.Sessions, &m.Tokens); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetJobID names the scheduled job a session ran.
func (d *DB) SetJobID(session, job string) error {
	_, err := d.Exec(`UPDATE session SET job_id=? WHERE id=?`, job, session)
	return err
}

// JobRuns returns the token totals of the latest finished runs of a job,
// newest first, leaving out one session.
func (d *DB) JobRuns(job, except string, limit int) ([]int64, error) {
	rows, err := d.Query(`SELECT total_tokens FROM session WHERE job_id=? AND id!=? AND ended_at IS NOT NULL AND ended_at!='' AND total_tokens>0 ORDER BY started_at DESC LIMIT ?`, job, except, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ClearReplay removes a session's replayed reproaches before it is read
// again. Live rows are kept.
func (d *DB) ClearReplay(session string) error {
	_, err := d.Exec(`DELETE FROM reproach WHERE session_id=? AND basis!='live'`, session)
	return err
}

// KnownRerun counts, per agent, checks that ran again with the same command
// after it passed, when only documentation (or nothing) was written since.
type KnownRerun struct {
	Agent           string `json:"agent"`
	Checks          int    `json:"checks"`
	Unchanged, Docs int
}

// KnownReruns reads the checks of sessions started since. A shell call
// that may write, or a write whose files are not known, ends the known
// result. isDoc tells documentation files.
func (d *DB) KnownReruns(since time.Time, isDoc func(string) bool) ([]KnownRerun, error) {
	rows, err := d.Query(`SELECT s.agent,e.session_id,e.seq,COALESCE(e.category,''),COALESCE(e.cmd_fp,''),e.exit_code,COALESCE(e.paths,'[]'),COALESCE(e.tool,'')
		FROM event e JOIN session s ON s.id=e.session_id WHERE s.started_at>=? AND e.kind='tool' AND e.category IN ('verify','produce','repair','revert')
		ORDER BY e.session_id,e.seq`, ts(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	by := map[string]*KnownRerun{}
	session := ""
	passed := map[string]string{} // cmd -> "unchanged" | "docs_only"
	for rows.Next() {
		var agent, sid, cat, cmd, paths, tool string
		var seq int64
		var exit sql.NullInt64
		if err := rows.Scan(&agent, &sid, &seq, &cat, &cmd, &exit, &paths, &tool); err != nil {
			return nil, err
		}
		if sid != session {
			session, passed = sid, map[string]string{}
		}
		r := by[agent]
		if r == nil {
			r = &KnownRerun{Agent: agent}
			by[agent] = r
		}
		if cat != "verify" {
			var ps []string
			_ = json.Unmarshal([]byte(paths), &ps)
			docs := len(ps) > 0
			for _, p := range ps {
				docs = docs && isDoc(p)
			}
			for k, state := range passed {
				if !docs {
					delete(passed, k)
				} else if state == "unchanged" {
					passed[k] = "docs_only"
				}
			}
			continue
		}
		if cmd == "" || tool != "shell" {
			continue
		}
		r.Checks++
		switch passed[cmd] {
		case "unchanged":
			r.Unchanged++
		case "docs_only":
			r.Docs++
		}
		if exit.Valid && exit.Int64 == 0 {
			passed[cmd] = "unchanged"
		} else {
			delete(passed, cmd)
		}
	}
	var out []KnownRerun
	for _, r := range by {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Checks > out[j].Checks })
	return out, rows.Err()
}
