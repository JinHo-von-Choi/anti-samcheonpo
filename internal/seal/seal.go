// Package seal implements Evidence Ledger v1 canonical serialization, the
// evidence hash chain and session seals.
package seal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

// SpecVersion is the Evidence Ledger version implemented here.
const SpecVersion = "evidence-ledger/1"

// Row is a canonical ledger row (event or verdict).
type Row struct {
	Kind  string `json:"kind"` // event | verdict
	Data  string `json:"data"` // canonical serialization
	Chain string `json:"chain"`
}

// Seal summarizes a session's ledger.
type Seal struct {
	Spec         string           `json:"spec"`
	SessionID    string           `json:"session_id"`
	Agent        string           `json:"agent"`
	SourceHash   string           `json:"source_hash"`
	ContractHash string           `json:"contract_hash"`
	Evaluator    string           `json:"evaluator"`
	PriceVersion string           `json:"price_version"`
	ConfigHash   string           `json:"config_hash"`
	Head         string           `json:"head"`
	Rows         int              `json:"rows"`
	TotalMicro   int64            `json:"total_micro_krw"`
	TotalTokens  int64            `json:"total_tokens"`
	BucketMicro  map[string]int64 `json:"bucket_micro_krw"`
}

// Short returns the first 12 hex chars of the head.
func (s Seal) Short() string {
	if len(s.Head) < 12 {
		return s.Head
	}
	return s.Head[:12]
}

func q(s string) string { b, _ := json.Marshal(s); return string(b) }

func list(xs []string) string {
	c := append([]string(nil), xs...)
	sort.Strings(c)
	parts := make([]string, len(c))
	for i, x := range c {
		parts[i] = q(x)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func ints(xs []int64) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.FormatInt(x, 10)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// PathID hides a path in canonical rows; the ledger keeps the plain path
// outside the chain so exported bundles reveal no paths.
func PathID(p string) string { return fp.Hash("path", p) }

func pathList(xs []string) string {
	ids := make([]string, len(xs))
	for i, x := range xs {
		ids[i] = PathID(x)
	}
	return list(ids)
}

func smap(m map[string]string) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = q(PathID(k)) + ":" + q(m[k])
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, ",") + "}"
}

// CanonicalEvent serializes the ledger fields of an event in the fixed
// Evidence Ledger v1 order. Raw text, commands and outputs are excluded.
func CanonicalEvent(ev *event.Event) string {
	exit := "null"
	if ev.ExitCode != nil {
		exit = strconv.Itoa(*ev.ExitCode)
	}
	ts := ""
	if !ev.TS.IsZero() {
		ts = ev.TS.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return fmt.Sprintf(`{"seq":%d,"ts":%s,"kind":%s,"tool":%s,"cmd_fp":%s,"paths":%s,"write_hashes":%s,"ws_before":%s,"ws_after":%s,"exit":%s,"err_fps":%s,"failed_tests":%s,"result_fp":%s,"in":%d,"out":%d,"cache_read":%d,"cache_write":%d,"cache_write_1h":%d,"model":%s,"cost_micro_krw":%d,"priced":%t,"category":%s,"bucket":%s,"symptom":%s,"estimated":%t,"forced":%t}`,
		ev.Seq, q(ts), q(string(ev.Kind)), q(ev.Tool), q(ev.CmdFP), pathList(ev.Paths), smap(ev.WriteHashes), q(ev.WSBefore), q(ev.WSAfter),
		exit, list(ev.ErrFPs), hashedList("test", ev.FailedTests), q(ev.ResultFP), ev.Usage.In, ev.Usage.Out, ev.Usage.CacheRead, ev.Usage.CacheWrite, ev.Usage.CacheWrite1h,
		q(ev.Usage.Model), ev.CostMicroKRW, ev.Priced, q(string(ev.Category)), q(ev.Bucket), q(ev.Symptom), ev.Estimated, ev.Forced)
}

// CanonicalVerdict serializes a verdict.
func CanonicalVerdict(v detect.Signal) string {
	return fmt.Sprintf(`{"seq":%d,"detector":%s,"rule":%s,"confidence":%s,"level":%d,"evidence":%s,"waste_micro_krw":%d,"primary":%t,"suppressed":%t,"estimate":%t}`,
		v.Seq, q(v.Detector), q(v.Rule), strconv.FormatFloat(v.Confidence, 'f', 2, 64), int(v.Level), ints(v.Evidence), v.WasteMicro, v.Primary, v.Suppressed, v.Estimate)
}

// Link computes chain = sha256(prev || 0x00 || data).
func Link(prev, data string) string {
	h := sha256.New()
	h.Write([]byte(prev))
	h.Write([]byte{0})
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

// Build creates the chained rows: events in seq order, then verdicts in order.
func Build(evs []*event.Event, vs []detect.Signal) []Row {
	var rows []Row
	prev := ""
	for _, ev := range evs {
		d := CanonicalEvent(ev)
		prev = Link(prev, d)
		rows = append(rows, Row{Kind: "event", Data: d, Chain: prev})
	}
	for _, v := range vs {
		d := CanonicalVerdict(v)
		prev = Link(prev, d)
		rows = append(rows, Row{Kind: "verdict", Data: d, Chain: prev})
	}
	return rows
}

// CheckChain recomputes a chain and returns the index of the first broken row (-1 if intact).
func CheckChain(rows []Row) int {
	prev := ""
	for i, r := range rows {
		prev = Link(prev, r.Data)
		if prev != r.Chain {
			return i
		}
	}
	return -1
}

// Head returns the last chain value.
func Head(rows []Row) string {
	if len(rows) == 0 {
		return Link("", "")
	}
	return rows[len(rows)-1].Chain
}

func hashedList(kind string, xs []string) string {
	ids := make([]string, len(xs))
	for i, x := range xs {
		ids[i] = fp.Hash(kind, x)
	}
	return list(ids)
}
