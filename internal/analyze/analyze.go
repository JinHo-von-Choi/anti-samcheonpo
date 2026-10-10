// Package analyze runs the retrospective pipeline: classify, price, virtual
// workspace fingerprints, detectors, hindsight reclassification and totals.
package analyze

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
)

// EvaluatorVersion identifies the detector rule set; recorded in receipt seals.
const EvaluatorVersion = "samcheonpo-eval/0.3.1"

// Buckets of the receipt.
const (
	BucketProgress = "progress"
	BucketExplore  = "explore"
	BucketWaste    = "waste"
	BucketOther    = "other"
	BucketWatch    = "watch"
)

// ProgressGrade.
const (
	GradeVerified   = "verified"
	GradeEstimated  = "estimated"
	GradeUnmeasured = "unmeasured"
)

// Result is the outcome of analyzing one session.
type Result struct {
	ReliabilityEvicted  uint64
	ReliabilityDeferred uint64
	Session             *event.Session
	Verdicts            []detect.Signal
	Totals              Totals
	Grade               string
	Criteria            struct{ Met, Total int }
	ConfigHash          string
	PriceVersion        string
	ContractHash        string
	FormatOK            bool
}

// Totals are micro-won and token sums per bucket and symptom.
type Totals struct {
	Micro           int64            `json:"micro_krw"`
	Tokens          int64            `json:"tokens"`
	UnpricedTokens  int64            `json:"unpriced_tokens"`
	BucketMicro     map[string]int64 `json:"bucket_micro_krw"`
	BucketTokens    map[string]int64 `json:"bucket_tokens"`
	SymptomMicro    map[string]int64 `json:"symptom_micro_krw"`
	SymptomTokens   map[string]int64 `json:"symptom_tokens"`
	SymptomCount    map[string]int   `json:"symptom_count"`
	EstimatedMicro  int64            `json:"estimated_micro_krw"`
	EstimatedTokens int64            `json:"estimated_tokens"`
	Interventions   map[string]int   `json:"interventions"`
	Minutes         int              `json:"minutes"`
}

// PriceCoverage is the share of tokens that were converted to won.
func (t Totals) PriceCoverage() float64 {
	if t.Tokens == 0 {
		return 1
	}
	return float64(t.Tokens-t.UnpricedTokens) / float64(t.Tokens)
}

// Options configures an analysis run.
type Options struct {
	Config     config.Config
	ConfigHash string
	Prices     *cost.Table
	Contract   *contract.Contract
	Accepted   bool
}

var rmRe = lazyre.New(`(?:^|&&|;|\|\|)\s*(?:git\s+)?rm\s+(?:-[a-zA-Z]+\s+)*([^;&|]+)`)

// Run analyzes a parsed session.
func Run(s *event.Session, opt Options) (*Result, error) {
	cfg := opt.Config
	copt := classify.Options{VerifyCommands: cfg.VerifyCommands}
	r := &Result{Session: s, ConfigHash: opt.ConfigHash, PriceVersion: opt.Prices.Version}
	if opt.Contract != nil {
		r.ContractHash = opt.Contract.ChecksHash()
	}
	r.FormatOK = formatOK(s)
	s.FormatFailed = !r.FormatOK
	for _, ev := range s.Events {
		if ev.Tool != "checkpoint_check" {
			classify.Initial(ev, copt)
		}
		if ev.Tool == event.ToolShell && ev.Category == event.CatProduce {
			for _, m := range rmRe.FindAllStringSubmatch(ev.CmdNorm, -1) {
				for _, f := range strings.Fields(m[1]) {
					f = strings.Trim(f, `'"`)
					if f == "" || strings.HasPrefix(f, "-") || strings.ContainsAny(f, "*$`") {
						continue
					}
					ev.Deleted = append(ev.Deleted, contract.NormalizePath(s.ProjectPath, joinDir(ev.Dir, f)))
				}
			}
		}
	}
	opt.Prices.Apply(s.Events)
	virtualWS(s.Events)
	eng := detect.NewEngine(cfg, opt.Contract, opt.Accepted, "audit", s.ProjectPath, s.ID, s.FirstPrompt)
	if !r.FormatOK {
		// format change stops judgement; costs are still summed
		reclassify(s, eng, r)
		return r, nil
	}
	for i, ev := range s.Events {
		if ev.Tool == "checkpoint_check" {
			eng.ObserveReliabilityFact(ev)
		} else {
			eng.Observe(ev)
		}
		if ev.Kind == event.KindMessage && turnEnds(s.Events, i) {
			eng.TurnEnd(ev, nil)
		}
	}
	r.ReliabilityEvicted, r.ReliabilityDeferred = eng.ReliabilityStats()
	r.Verdicts = eng.Verdicts
	reclassify(s, eng, r)
	return r, nil
}

func joinDir(dir, f string) string {
	if dir == "" || pathnorm.IsAbs(f) {
		return f
	}
	return strings.TrimSuffix(dir, "/") + "/" + f
}

// turnEnds reports whether events[i] is the last assistant message of a turn.
func turnEnds(evs []*event.Event, i int) bool {
	for j := i + 1; j < len(evs); j++ {
		switch evs[j].Kind {
		case event.KindPrompt, event.KindStop, event.KindSessionEnd:
			return true
		case event.KindTool, event.KindMessage:
			return false
		}
	}
	return true
}

// formatOK applies the core-item parse-rate check.
// A few unpaired calls are normal (a session ending or being interrupted
// mid-call, per merged subagent), so up to 3 misses are tolerated.
func formatOK(s *event.Session) bool {
	if miss := s.ToolUses - s.ToolUsesPaired; miss > 3 && float64(s.ToolUsesPaired)/float64(s.ToolUses) < 0.95 {
		return false
	}
	if miss := s.UsageLines - s.UsageParsed; miss > 3 && float64(s.UsageParsed)/float64(s.UsageLines) < 0.95 {
		return false
	}
	return true
}

// virtualWS computes retrospective workspace fingerprints.
func virtualWS(evs []*event.Event) {
	files := map[string]string{}
	var marks []string
	cur := fp.MapFP(files, marks)
	for _, ev := range evs {
		ev.WSBefore = cur
		changed := false
		for p, h := range ev.WriteHashes {
			files[p] = h
			changed = true
		}
		for _, p := range ev.Deleted {
			delete(files, p)
			changed = true
		}
		if ev.Tool == event.ToolShell && ev.Mutating && len(ev.WriteHashes) == 0 {
			marks = append(marks, fmt.Sprintf("shell_mutation:%d", ev.Seq))
			changed = true
		}
		if changed {
			cur = fp.MapFP(files, marks)
		}
		ev.WSAfter = cur
	}
}

// reclassify performs hindsight reclassification and totals.
func reclassify(s *event.Session, eng *detect.Engine, r *Result) {
	st := eng.St
	revert := map[int64]bool{}
	s4 := map[int64]bool{}
	for _, v := range eng.Verdicts {
		switch v.Rule {
		case "s2.oscillation":
			for _, sq := range v.Evidence[1:] {
				revert[sq] = true
			}
		case "s4.read_only_streak":
			for _, sq := range v.Evidence {
				s4[sq] = true
			}
		}
	}
	// repair: re-editing a file after a failed verification that followed its previous edit
	repair := map[int64]bool{}
	lastWrite := map[string]int64{}
	lastFail := int64(-1)
	for _, ev := range s.Events {
		if ev.Category == event.CatVerify && ev.ExitCode != nil && *ev.ExitCode > 0 {
			lastFail = ev.Seq
		}
		if ev.Category != event.CatProduce {
			continue
		}
		for _, p := range ev.Paths {
			if lw, ok := lastWrite[p]; ok && lastFail > lw && !revert[ev.Seq] {
				repair[ev.Seq] = true
			}
			lastWrite[p] = ev.Seq
		}
	}
	// index of next produce event for "needed exploration"
	nextProduce := make([]int, len(s.Events))
	np := -1
	for i := len(s.Events) - 1; i >= 0; i-- {
		nextProduce[i] = np
		if s.Events[i].Category == event.CatProduce && !revert[s.Events[i].Seq] {
			np = i
		}
	}
	t := Totals{BucketMicro: map[string]int64{}, BucketTokens: map[string]int64{}, SymptomMicro: map[string]int64{},
		SymptomTokens: map[string]int64{}, SymptomCount: map[string]int{}, Interventions: map[string]int{}}
	for i, ev := range s.Events {
		sym, wasted := st.Wasted[ev.Seq]
		switch {
		case ev.Category == event.CatWatch:
			ev.Bucket = BucketWatch
		case wasted:
			ev.Bucket, ev.Symptom = BucketWaste, sym
		case revert[ev.Seq]:
			ev.Bucket, ev.Symptom = BucketWaste, "revert"
		case ev.Category == event.CatProduce && !repair[ev.Seq]:
			if ev.Mutating && ev.Tool == event.ToolShell && len(ev.Paths) == 0 {
				ev.Bucket = BucketOther
			} else {
				ev.Bucket = BucketProgress
			}
		case ev.Category == event.CatVerify && st.ProgressSeqs[ev.Seq]:
			ev.Bucket = BucketProgress
		case ev.Category == event.CatExplore && nextProduce[i] >= 0 && nextProduce[i]-i <= 20:
			ev.Bucket = BucketExplore
		case ev.Category == event.CatExplore && s4[ev.Seq] && st.Produced():
			ev.Bucket, ev.Symptom = BucketWaste, "S4"
		default:
			ev.Bucket = BucketOther
		}
		if ev.Category == event.CatProduce && ev.Bucket == BucketProgress {
			ev.Category = event.CatProduce
		} else if repair[ev.Seq] {
			ev.Category = event.CatRepair
		} else if revert[ev.Seq] {
			ev.Category = event.CatRevert
		}
		ev.Estimated = st.Estimated[ev.Seq]
		tok := ev.Usage.Total()
		t.Tokens += tok
		if !ev.Priced {
			t.UnpricedTokens += tok
		}
		t.Micro += ev.CostMicroKRW
		t.BucketMicro[ev.Bucket] += ev.CostMicroKRW
		t.BucketTokens[ev.Bucket] += tok
		if ev.Bucket == BucketWaste {
			t.SymptomMicro[ev.Symptom] += ev.CostMicroKRW
			t.SymptomTokens[ev.Symptom] += tok
			t.SymptomCount[ev.Symptom]++
		}
		if ev.Estimated {
			t.EstimatedMicro += ev.CostMicroKRW
			t.EstimatedTokens += tok
		}
	}
	for _, v := range eng.Verdicts {
		// shadow rules are measured, never delivered, so they are not
		// interventions that would have happened
		if v.Primary && v.Level > detect.L0 && !slices.Contains(eng.Cfg.Rollout.ShadowRules, v.Rule) {
			t.Interventions[v.Level.String()]++
		}
	}
	if !s.StartedAt.IsZero() && s.EndedAt.After(s.StartedAt) {
		t.Minutes = int(s.EndedAt.Sub(s.StartedAt).Minutes())
	}
	r.Totals = t
	r.Grade = GradeUnmeasured
	if len(st.ProgressSeqs) > 0 || len(st.GrowthSeqs) > 0 {
		r.Grade = GradeEstimated
	}
}

// Finalize reclassifies a live session from the engine state collected
// during the session (SessionEnd).
func Finalize(s *event.Session, eng *detect.Engine, cfgHash, priceVersion string) *Result {
	r := &Result{Session: s, ConfigHash: cfgHash, PriceVersion: priceVersion, FormatOK: true}
	// live events are observed by a worker and by the checkpoint runner;
	// hindsight rules index by sequence order
	evs := append([]*event.Event(nil), eng.St.Events...)
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].Seq < evs[j].Seq })
	s.Events = evs
	r.Verdicts = eng.Verdicts
	reclassify(s, eng, r)
	return r
}

// CheckInvariant verifies the sum invariant.
func CheckInvariant(s *event.Session, t Totals) error {
	var micro, tok int64
	for _, ev := range s.Events {
		micro += ev.CostMicroKRW
		tok += ev.Usage.Total()
	}
	var bm, bt int64
	for _, v := range t.BucketMicro {
		bm += v
	}
	for _, v := range t.BucketTokens {
		bt += v
	}
	if bm != micro || bm != t.Micro {
		return fmt.Errorf("합계 불변식 위반: 분류 합계 %d != 세션 합계 %d (마이크로원)", bm, micro)
	}
	if bt != tok {
		return fmt.Errorf("합계 불변식 위반: 분류 토큰 %d != 세션 토큰 %d", bt, tok)
	}
	return nil
}

// SortedSymptoms returns symptom keys by descending cost.
func SortedSymptoms(t Totals) []string {
	var ks []string
	for k := range t.SymptomMicro {
		ks = append(ks, k)
	}
	for k := range t.SymptomTokens {
		if _, ok := t.SymptomMicro[k]; !ok {
			ks = append(ks, k)
		}
	}
	sort.Slice(ks, func(i, j int) bool {
		a, b := t.SymptomMicro[ks[i]], t.SymptomMicro[ks[j]]
		if a != b {
			return a > b
		}
		if t.SymptomTokens[ks[i]] != t.SymptomTokens[ks[j]] {
			return t.SymptomTokens[ks[i]] > t.SymptomTokens[ks[j]]
		}
		return ks[i] < ks[j]
	})
	return ks
}

// SymptomTotal is the total waste in micro-won.
func (t Totals) SymptomTotal() int64 {
	var s int64
	for _, v := range t.SymptomMicro {
		s += v
	}
	return s
}

// SymptomTokenTotal is the total waste in tokens.
func (t Totals) SymptomTokenTotal() int64 {
	var s int64
	for _, v := range t.SymptomTokens {
		s += v
	}
	return s
}
