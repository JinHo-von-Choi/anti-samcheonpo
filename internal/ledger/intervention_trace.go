package ledger

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
)

type InterventionMetadata struct {
	PolicyVersion   string       `json:"policy_version,omitempty"`
	Experiment      string       `json:"experiment,omitempty"`
	RequestedAction string       `json:"requested_action,omitempty"`
	EffectiveAction string       `json:"effective_action,omitempty"`
	Phase           string       `json:"phase,omitempty"`
	Reason          string       `json:"reason,omitempty"`
	HostVersion     string       `json:"host_version,omitempty"`
	AdapterVersion  string       `json:"adapter_version,omitempty"`
	DetectorVersion string       `json:"detector_version,omitempty"`
	ConfigDigest    string       `json:"config_digest,omitempty"`
	Model           string       `json:"model,omitempty"`
	Capabilities    adapter.Caps `json:"capabilities"`
	Evidence        []int64      `json:"evidence,omitempty"`
}

type InterventionTrace struct {
	ID           string               `json:"intervention_id"`
	VerdictID    string               `json:"verdict_id"`
	SessionID    string               `json:"session_id"`
	Rule         string               `json:"rule"`
	Channel      string               `json:"channel"`
	Arm          string               `json:"arm"`
	Delivery     string               `json:"delivery_state"`
	EmittedAt    string               `json:"emitted_at,omitempty"`
	DeliveredAt  string               `json:"printed_at,omitempty"`
	HostReceived string               `json:"host_received"`
	Blocked      bool                 `json:"blocked"`
	Metadata     InterventionMetadata `json:"metadata"`
	Recoveries   []recovery.Attempt   `json:"recoveries,omitempty"`
}

func (d *DB) RecordInterventionDetailed(v detect.Signal, session, channel string, blocked bool, meta InterventionMetadata) error {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	_, err = d.Exec(`INSERT INTO intervention(verdict_id,session_id,seq,channel,level,arm,delivery_state,rule,blocked,intervention_id,metadata) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, v.ID, session, v.Seq, channel, int(v.Level), v.Arm, "proposed", v.Rule, b2i(blocked), hex.EncodeToString(id[:]), string(b))
	return err
}

// Printed is a client transport receipt, not proof the host model read it.
func (d *DB) MarkInterventionOutput(session, verdict string, printed bool, at time.Time) error {
	ts := at.UTC().Format(time.RFC3339Nano)
	if printed {
		_, err := d.Exec(`UPDATE intervention SET delivery_state='delivered',emitted_at=COALESCE(emitted_at,?),delivered_at=COALESCE(delivered_at,?) WHERE session_id=? AND verdict_id=?`, ts, ts, session, verdict)
		return err
	}
	_, err := d.Exec(`UPDATE intervention SET delivery_state=CASE WHEN delivery_state='delivered' THEN delivery_state ELSE 'emitted' END,emitted_at=COALESCE(emitted_at,?) WHERE session_id=? AND verdict_id=?`, ts, session, verdict)
	return err
}

func (d *DB) InterventionTraces(session, verdict string) ([]InterventionTrace, error) {
	rows, err := d.Query(`SELECT intervention_id,verdict_id,session_id,rule,channel,COALESCE(arm,''),delivery_state,COALESCE(emitted_at,''),COALESCE(delivered_at,''),blocked,metadata FROM intervention WHERE session_id=? AND (?='' OR verdict_id=?) ORDER BY rowid`, session, verdict, verdict)
	if err != nil {
		return nil, err
	}
	var out []InterventionTrace
	for rows.Next() {
		var r InterventionTrace
		var raw string
		if err = rows.Scan(&r.ID, &r.VerdictID, &r.SessionID, &r.Rule, &r.Channel, &r.Arm, &r.Delivery, &r.EmittedAt, &r.DeliveredAt, &r.Blocked, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &r.Metadata); err != nil {
			rows.Close()
			return nil, err
		}
		r.HostReceived = "unknown"
		out = append(out, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = d.Query(`SELECT payload FROM recovery_attempt WHERE session_id=? ORDER BY rowid`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byVerdict := map[string][]recovery.Attempt{}
	for rows.Next() {
		var raw string
		var a recovery.Attempt
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &a); err != nil {
			return nil, err
		}
		byVerdict[a.VerdictID] = append(byVerdict[a.VerdictID], a)
	}
	for i := range out {
		out[i].Recoveries = byVerdict[out[i].VerdictID]
	}
	return out, rows.Err()
}
