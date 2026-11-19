package ledger

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// FunnelRow counts one rule's verdicts at each step from judgment to an
// observed effect. A verdict that could have reached the agent or the user
// but did not carries exactly one reason in Reasons.
type FunnelRow struct {
	Rule string `json:"rule"`
	// Verdicts is every verdict the rule produced.
	Verdicts int `json:"verdicts"`
	// Observation counts level 0 verdicts and those merged into another
	// verdict on the same event: they are records, not messages.
	Observation int `json:"observation"`
	// Candidates are the verdicts meant to be delivered.
	Candidates int `json:"candidates"`
	Delivered  int `json:"delivered"`
	Blocked    int `json:"blocked"`
	// Effect counts delivered verdicts whose follow-up was observed.
	Effect  int            `json:"effect_observed"`
	Reasons map[string]int `json:"undelivered_reasons"`
	// Reproach counts this rule's verdicts in a span the user scolded with
	// a matching theme; MissedReproach is filled per theme, not per rule.
	Reproach int `json:"reproach_matched"`
}

// Undelivered reasons the funnel derives from the ledger itself, beside the
// decision codes the live session records.
const (
	ReasonCooldown    = "cooldown"
	ReasonShadow      = "shadow"
	ReasonNotCarried  = "not_carried"
	ReasonUnconfirmed = "emitted_unconfirmed"
	ReasonOffline     = "offline_analysis"
	ReasonNone        = "no_reason"
)

type funnelVerdict struct {
	session, rule, mode, ended string
	seq                        int64
	primary, suppressed        bool
	level                      int
	delivered, blocked, shadow bool
	proposed, emitted          bool
	hasIntervention            bool
}

// DeliveryFunnel counts, per rule, verdicts of sessions started at or after
// since. It reads delivery decisions from event observations, so it is a
// report for the command line, not for a hook.
func (d *DB) DeliveryFunnel(since time.Time) ([]FunnelRow, error) {
	rows, err := d.Query(`SELECT v.id,v.session_id,v.seq,v.rule,v.is_primary,v.level,v.suppressed,s.mode,COALESCE(s.ended_at,''),
		COALESCE(i.channel,''),COALESCE(i.delivery_state,''),COALESCE(i.blocked,0)
		FROM verdict v JOIN session s ON s.id=v.session_id LEFT JOIN intervention i ON i.verdict_id=v.id AND i.session_id=v.session_id
		WHERE s.started_at>=? ORDER BY v.session_id,v.seq`, ts(since))
	if err != nil {
		return nil, err
	}
	byID := map[string]*funnelVerdict{}
	var order []string
	for rows.Next() {
		var id, channel, state string
		var primary, suppressed, blocked int
		v := funnelVerdict{}
		if err := rows.Scan(&id, &v.session, &v.seq, &v.rule, &primary, &v.level, &suppressed, &v.mode, &v.ended, &channel, &state, &blocked); err != nil {
			rows.Close()
			return nil, err
		}
		cur := byID[id]
		if cur == nil {
			v.primary, v.suppressed = primary == 1, suppressed == 1
			cur = &v
			byID[id] = cur
			order = append(order, id)
		}
		if channel == "" {
			continue
		}
		cur.hasIntervention = true
		switch {
		case blocked == 1:
			cur.blocked = true
		case channel == "shadow" || channel == "observe":
			cur.shadow = true
		case state == "delivered":
			cur.delivered = true
		case state == "emitted":
			cur.emitted = true
		default:
			cur.proposed = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	effects, err := d.observedEffects(since)
	if err != nil {
		return nil, err
	}
	decisions := map[string]map[int64][]string{}
	out := map[string]*FunnelRow{}
	for _, id := range order {
		v := byID[id]
		r := out[v.rule]
		if r == nil {
			r = &FunnelRow{Rule: v.rule, Reasons: map[string]int{}}
			out[v.rule] = r
		}
		r.Verdicts++
		if !v.primary || v.level == 0 {
			r.Observation++
			continue
		}
		r.Candidates++
		if v.blocked {
			r.Blocked++
		}
		if v.delivered {
			r.Delivered++
			if effects[id] {
				r.Effect++
			}
		}
		if v.blocked || v.delivered {
			continue
		}
		reason := ""
		switch {
		case v.suppressed:
			reason = ReasonCooldown
		case v.shadow:
			reason = ReasonShadow
		case v.emitted:
			reason = ReasonUnconfirmed
		case v.proposed:
			reason = ReasonNotCarried
		}
		if reason == "" {
			codes, ok := decisions[v.session]
			if !ok {
				if codes, err = d.decisionCodes(v.session); err != nil {
					return nil, err
				}
				decisions[v.session] = codes
			}
			for _, c := range codes[v.seq] {
				if code, ok := strings.CutPrefix(c, v.rule+"\x00"); ok && code != "" {
					reason = code
					break
				}
			}
		}
		if reason == "" && v.mode == "audit" {
			reason = ReasonOffline
		}
		if reason == "" {
			reason = ReasonNone
		}
		r.Reasons[reason]++
	}
	res := make([]FunnelRow, 0, len(out))
	for _, r := range out {
		res = append(res, *r)
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].Verdicts != res[j].Verdicts {
			return res[i].Verdicts > res[j].Verdicts
		}
		return res[i].Rule < res[j].Rule
	})
	return res, nil
}

// decisionCodes returns, per event, "rule\x00code" for each delivery decision.
// The first decision a session recorded for a rule is the reason: later ones
// (a cooldown after a refusal) describe the same verdict again.
func (d *DB) decisionCodes(session string) (map[int64][]string, error) {
	rows, err := d.Query(`SELECT seq,payload FROM event_observation WHERE session_id=?`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	res := map[int64][]string{}
	for rows.Next() {
		var seq int64
		var payload string
		if err := rows.Scan(&seq, &payload); err != nil {
			return nil, err
		}
		var p struct {
			Decisions []struct {
				Rule string `json:"rule"`
				Code string `json:"code"`
			} `json:"decisions"`
		}
		if json.Unmarshal([]byte(payload), &p) != nil {
			continue
		}
		for _, dc := range p.Decisions {
			res[seq] = append(res[seq], dc.Rule+"\x00"+dc.Code)
		}
	}
	return res, rows.Err()
}

// observedEffects returns the verdicts whose recovery attempt reached an
// observed effect.
func (d *DB) observedEffects(since time.Time) (map[string]bool, error) {
	rows, err := d.Query(`SELECT r.payload FROM recovery_attempt r JOIN session s ON s.id=r.session_id WHERE s.started_at>=?`, ts(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	res := map[string]bool{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var a struct {
			VerdictID string `json:"verdict_id"`
			Stage     string `json:"stage"`
		}
		if json.Unmarshal([]byte(payload), &a) == nil && a.Stage == "effect_observed" {
			res[a.VerdictID] = true
		}
	}
	return res, rows.Err()
}
