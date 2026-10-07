// Package detect implements detectors S1-S8, signal combination and cooldown.
// Detectors are deterministic: the same event sequence yields the same signals;
// time comes only from event timestamps.
package detect

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

// Level is an intervention level L0-L4.
type Level int

const (
	L0 Level = iota
	L1
	L2
	L3
	L4
)

func (l Level) String() string { return fmt.Sprintf("L%d", int(l)) }

// Signal is one detector finding.
type Signal struct {
	ID         string         `json:"id"`
	Detector   string         `json:"detector"`
	Rule       string         `json:"rule"`
	Confidence float64        `json:"confidence"`
	Evidence   []int64        `json:"evidence"`
	WasteMicro int64          `json:"waste_micro_krw"`
	Level      Level          `json:"level"`
	Seq        int64          `json:"seq"` // event at which the signal was raised
	Facts      map[string]any `json:"facts,omitempty"`
	Primary    bool           `json:"primary"`              // the one delivered for its event
	Arm        string         `json:"arm,omitempty"`        // experiment arm for L1 signals
	Estimate   bool           `json:"estimate,omitempty"`   // estimate-only finding (확인 필요)
	Suppressed bool           `json:"suppressed,omitempty"` // in cooldown, not delivered
}

// Engine runs all detectors over a session's events.
type Engine struct {
	Cfg      config.Config
	Contract *contract.Contract
	Accepted bool
	Mode     string // live | audit
	Root     string
	Session  string
	// FirstPrompt is used to infer scope in audit mode.
	FirstPrompt string

	St       *State
	Verdicts []Signal

	cooldownUntil map[string]int64
	cooldownLevel map[string]Level
	escalate      map[string]Level
	nextID        int
}

// State is the per-session detector state.
type State struct {
	usageCache   usageCache
	Events       []*event.Event
	bySeq        map[int64]*event.Event
	TotalMicro   int64
	ProgressMark int64 // total at last progress
	LastProgress int64 // seq
	ProgressSeqs map[int64]bool
	// envStreak counts failures that need an action outside the code, by
	// failure class and command, with the workspace state of the last one.
	envStreak map[string]*streak
	// stuckCmd holds command fingerprints with a raised stuck-error streak;
	// hiding holds error-hiding kinds already raised per path. PreCheck uses
	// both to flag a repeat before it runs.
	// lastRun is the latest run per command fingerprint; reviews counts
	// reviews and subagent calls per workspace state and purpose.
	lastRun  map[string]runMark
	reviews  map[string]int
	stuckCmd map[string]bool
	hiding   map[string]map[string]bool
	// lastFail is the failure class and workspace of the latest failed run
	// per command fingerprint; PreCheck uses it to block unchanged reruns.
	lastFail map[string]failMark
	// GrowthSeqs are code growth while a verification was failing: estimated
	// output for receipts, but not progress for idle budgets or ratios.
	GrowthSeqs map[int64]bool
	Wasted     map[int64]string // seq -> symptom
	Estimated  map[int64]bool

	toolWin        []*event.Event
	verify         map[string]*vcEntry
	errStreak      map[string]*streak
	failSeries     []failPoint
	fileHist       map[string][]hist
	editHist       map[string][]editHist
	lineCount      map[string]int // estimated current lines per file
	highWater      map[string]int
	created        map[string]bool
	explore        []int64
	lastProduceTS  time.Time
	sessionStart   time.Time
	readCount      map[string]int
	reads, rereads int
	readsHalf      []int // rereads per event index bucket for trend
	compacts       int
	reinserts      int
	minute         map[int64]int64
	outScope       map[string]bool
	// guessOut counts writes outside the guessed scope per directory group;
	// prevGuess is the guessed scope of the previous intent revision, used to
	// spot a return to an old goal after context compaction.
	guessOut             map[string]int
	prevGuess            []string
	compacted            bool
	allowGuess           []string
	scopeKnown           bool
	lastVerify           *event.Event
	lastWriteSeq         int64
	lastCodeWriteSeq     int64
	writes               int
	testAdded, codeAdded int
	testLits             map[string]int64
	lastUnknown          int64
	forced               int
	forcedSeqs           []int64
	idleFired            bool
	bloatFired           bool
	rotFired             bool
	velocityFired        map[int64]bool
	budgetFired          map[int]bool
	lastFailFP           string
	// S6: files written since the last failing verification, and the
	// strategies (distinct file sets) that ended in the same error family
	attemptFiles map[string]bool
	strategies   []strategy
	s6Fired      bool
	s6SameFired  bool
	// recoveryUsed marks the one recovery check granted per run and cause.
	recoveryUsed map[string]bool
}

// sameFilesAttempts is when repeated failures on the same files suggest a
// change of approach: one after the first stuck-error advice (S2, third
// attempt), so the two never compete for the same event.
const sameFilesAttempts = 4

type strategy struct {
	files  string
	family map[string]bool
	seq    int64
	// count is consecutive failures with this file set; failed is the
	// failing-test count of the latest one (fewer failures is new evidence)
	count  int
	failed int
}

type vcEntry struct {
	result string
	count  int
	seqs   []int64
}

type streak struct {
	count  int
	lastWS string
	seqs   []int64
}

type failPoint struct {
	errKey string
	failed int
	seq    int64
}

type hist struct {
	hash string
	seq  int64
}

type editHist struct {
	old, new string
	seq      int64
}

// NewEngine creates an engine.
func NewEngine(cfg config.Config, c *contract.Contract, accepted bool, mode, root, session, firstPrompt string) *Engine {
	e := &Engine{Cfg: cfg, Contract: c, Accepted: accepted, Mode: mode, Root: root, Session: session, FirstPrompt: firstPrompt,
		cooldownUntil: map[string]int64{}, cooldownLevel: map[string]Level{}, escalate: map[string]Level{}}
	e.St = &State{
		bySeq: map[int64]*event.Event{}, ProgressSeqs: map[int64]bool{}, GrowthSeqs: map[int64]bool{}, envStreak: map[string]*streak{}, stuckCmd: map[string]bool{}, lastRun: map[string]runMark{}, reviews: map[string]int{}, hiding: map[string]map[string]bool{}, lastFail: map[string]failMark{}, Wasted: map[int64]string{}, Estimated: map[int64]bool{},
		verify: map[string]*vcEntry{}, errStreak: map[string]*streak{}, fileHist: map[string][]hist{},
		editHist: map[string][]editHist{}, lineCount: map[string]int{}, highWater: map[string]int{}, created: map[string]bool{},
		readCount: map[string]int{}, minute: map[int64]int64{}, outScope: map[string]bool{}, guessOut: map[string]int{}, testLits: map[string]int64{},
		velocityFired: map[int64]bool{}, budgetFired: map[int]bool{}, LastProgress: -1, lastUnknown: -1, attemptFiles: map[string]bool{}, recoveryUsed: map[string]bool{},
	}
	if c != nil && len(c.Scope.Allow) > 0 {
		e.St.scopeKnown = true
	} else {
		e.St.allowGuess = workScope(firstPrompt)
	}
	return e
}

var pathTokenRe = lazyre.New("(?:^|[\\s`'\"(\\[])((?:[A-Za-z0-9_.@-]+/)+[A-Za-z0-9_.@*-]*|[A-Za-z0-9_-]+\\.(?:go|py|ts|tsx|js|jsx|java|kt|rs|rb|php|cs|swift|md|yml|yaml|json|toml|html|css|sql|sh|vue|svelte))")

// GuessScope infers allowed paths from a first prompt (audit mode).
func GuessScope(prompt string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range pathTokenRe.FindAllStringSubmatch(prompt, -1) {
		p := strings.Trim(m[1], "/.")
		if p == "" || strings.HasPrefix(p, "http") || strings.Contains(p, "://") {
			continue
		}
		if strings.HasPrefix(m[1], "/") {
			continue
		}
		var g string
		if strings.Contains(path.Base(p), ".") {
			g = path.Dir(p)
			if g == "." {
				g = p
			} else {
				g += "/**"
			}
		} else {
			g = p + "/**"
		}
		if !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	return out
}

func (e *Engine) add(sigs *[]Signal, s Signal) {
	*sigs = append(*sigs, s)
}

// Observe processes one event and returns the combined signals for it.
func (e *Engine) Observe(ev *event.Event) []Signal {
	st := e.St
	st.Events = append(st.Events, ev)
	st.bySeq[ev.Seq] = ev
	if st.sessionStart.IsZero() && !ev.TS.IsZero() {
		st.sessionStart = ev.TS
		st.lastProduceTS = ev.TS
	}
	st.TotalMicro += ev.CostMicroKRW
	if !ev.TS.IsZero() {
		st.minute[ev.TS.Unix()/60] += ev.CostMicroKRW
	}
	var sigs []Signal
	switch ev.Kind {
	case event.KindCompact:
		st.compacts++
		st.compacted = true
		e.memoryRot(ev, &sigs)
	case event.KindPrompt:
		if ev.Forced {
			e.forcedTurn(ev, &sigs)
		}
	case event.KindTool:
		e.tool(ev, &sigs)
	}
	e.cost(ev, &sigs)
	return e.combine(ev, sigs)
}

func (e *Engine) tool(ev *event.Event, sigs *[]Signal) {
	st := e.St
	st.toolWin = append(st.toolWin, ev)
	if len(st.toolWin) > e.Cfg.Detectors.S1.RatioWindow {
		st.toolWin = st.toolWin[1:]
	}
	if ev.Tool == event.ToolShell && ev.Unknown {
		st.lastUnknown = ev.Seq
	}
	switch ev.Category {
	case event.CatVerify:
		e.verification(ev, sigs)
	case event.CatProduce:
		e.production(ev, sigs)
	case event.CatExplore:
		e.exploration(ev, sigs)
	}
	e.environment(ev, sigs)
	e.reviewRepeat(ev, sigs)
	if ev.Tool == event.ToolShell && OrTrueRE.MatchString(ev.CmdNorm) && ev.Category == event.CatVerify {
		e.add(sigs, Signal{Detector: "S5", Rule: "s5.error_hiding", Confidence: 0.8, Level: L1, Evidence: []int64{ev.Seq},
			Facts: map[string]any{"kind": "or_true", "cmd": ev.CmdNorm}})
	}
	e.verifyRatio(ev, sigs)
	e.memoryRot(ev, sigs)
}

func (e *Engine) nondeterministic(norm string) bool {
	for _, p := range e.Cfg.NondeterministicCommands {
		if p == "" {
			continue
		}
		if ok, _ := path.Match(p, norm); ok || strings.HasPrefix(norm, p) {
			return true
		}
	}
	return false
}

// ExecutedVerify reports whether ev is a verification that actually ran.
func ExecutedVerify(ev *event.Event) bool {
	return ev.Category == event.CatVerify && ev.ExitCode != nil && *ev.ExitCode != -1
}

func failing(ev *event.Event) bool { return ev.ExitCode != nil && *ev.ExitCode > 0 }

// runID is the identity of a shell run for repeat decisions: the execution
// fingerprint when the adapter computed one, else the display fingerprint.
func runID(ev *event.Event) string {
	if ev.ExecFP != "" {
		return ev.ExecFP
	}
	return ev.CmdFP
}

// blockableRun reports whether a run's identity is certain enough to block a
// repeat. Runs without an execution fingerprint (older records) are advised
// only; so are runs whose shell text hides what would execute.
func blockableRun(ev *event.Event) bool { return ev.ExecFP != "" && ev.ExecCertain }

func (e *Engine) verification(ev *event.Event, sigs *[]Signal) {
	st := e.St
	if !ExecutedVerify(ev) {
		return
	}
	prev := st.lastVerify
	st.lastVerify = ev
	e.verifyAfterDocs(ev, sigs)
	// progress: result improved versus the previous run of the same command
	e.verifyProgress(ev, prev)

	// S1 identical rerun
	if runID(ev) != "" && !e.nondeterministic(ev.CmdNorm) {
		key := runID(ev) + "|" + ev.WSBefore
		ent := st.verify[key]
		if ent != nil && ent.result == ev.ResultFP {
			ent.count++
			ent.seqs = append(ent.seqs, ev.Seq)
			conf := 0.95
			if st.lastUnknown > ent.seqs[0] {
				conf -= 0.15
			}
			waste := e.markWaste([]int64{ev.Seq}, "S1")
			lvl := L0
			if ent.count >= e.threshold("s1.identical_rerun", e.Cfg.Detectors.S1.RepeatNudgeAt) {
				lvl = L1
			}
			e.add(sigs, Signal{Detector: "S1", Rule: "s1.identical_rerun", Confidence: conf, Level: lvl, WasteMicro: waste,
				Evidence: append([]int64(nil), ent.seqs...),
				Facts:    map[string]any{"cmd": ev.CmdNorm, "count": ent.count, "failed_tests": ev.FailedTests, "exit": *ev.ExitCode}})
		} else {
			st.verify[key] = &vcEntry{result: ev.ResultFP, count: 1, seqs: []int64{ev.Seq}}
		}
	}

	// S2 stuck error / whack-a-mole
	keys := append([]string(nil), ev.ErrFPs...)
	for _, t := range ev.FailedTests {
		keys = append(keys, "test:"+t)
	}
	if failing(ev) && len(keys) > 0 {
		cur := map[string]bool{}
		for _, f := range keys {
			cur[f] = true
			s := st.errStreak[f]
			if s == nil {
				s = &streak{lastWS: "\x00"}
				st.errStreak[f] = s
			}
			if s.lastWS != ev.WSBefore {
				s.count++
				s.lastWS = ev.WSBefore
				s.seqs = append(s.seqs, ev.Seq)
				p := e.Cfg.Detectors.S2
				// a raised nudge threshold shifts the later steps by the same
				// amount, so the ladder never goes L2 before L1
				nudge := e.threshold("s2.stuck_error", p.Nudge)
				shift := nudge - p.Nudge
				notify, pause := p.Notify+shift, p.Pause+shift
				var lvl Level = -1
				switch s.count {
				case nudge:
					lvl = L1
				case notify:
					lvl = L2
				case pause:
					lvl = L3
				}
				if s.count > pause {
					lvl = L3
				}
				if lvl >= 0 {
					// attempts after the first one, including the edits in between
					var rng []int64
					for _, sq := range st.Events {
						if sq.Seq > s.seqs[0] && sq.Seq <= ev.Seq {
							rng = append(rng, sq.Seq)
						}
					}
					waste := e.markWaste(rng, "S2")
					st.stuckCmd[runID(ev)] = true
					e.add(sigs, Signal{Detector: "S2", Rule: "s2.stuck_error", Confidence: 0.9, Level: lvl, WasteMicro: waste,
						Evidence: append([]int64(nil), s.seqs...),
						Facts:    map[string]any{"count": s.count, "cmd": ev.CmdNorm, "error": errorLabel(ev)}})
				}
			}
		}
		for f := range st.errStreak {
			if !cur[f] {
				delete(st.errStreak, f)
			}
		}
		errKey := strings.Join(ev.ErrFPs, ",")
		if errKey == "" {
			errKey = strings.Join(ev.FailedTests, ",")
		}
		if len(st.failSeries) == 0 || st.failSeries[len(st.failSeries)-1].seq != ev.Seq {
			last := ""
			if n := len(st.failSeries); n > 0 {
				last = st.failSeries[n-1].errKey
			}
			_ = last
			st.failSeries = append(st.failSeries, failPoint{errKey: errKey, failed: len(ev.FailedTests), seq: ev.Seq})
		}
		e.whackAMole(ev, sigs)
		e.capabilityLimit(ev, keys, sigs)
	} else if !failing(ev) {
		st.errStreak = map[string]*streak{}
		st.failSeries = nil
		st.strategies = nil
		st.attemptFiles = map[string]bool{}
	}
}

func errorLabel(ev *event.Event) string {
	if len(ev.FailedTests) > 0 {
		return ev.FailedTests[0]
	}
	return ""
}

func (e *Engine) whackAMole(ev *event.Event, sigs *[]Signal) {
	st := e.St
	n := e.Cfg.Detectors.S2.WhackAttempts
	if len(st.failSeries) < n {
		return
	}
	w := st.failSeries[len(st.failSeries)-n:]
	changes := 0
	for i := 1; i < len(w); i++ {
		if w[i].errKey != w[i-1].errKey {
			changes++
		}
	}
	first := w[0].failed
	if first == 0 || changes < n-2 {
		return
	}
	for _, p := range w[1:] {
		if p.failed < first {
			return
		}
	}
	var seqs []int64
	for _, p := range w {
		seqs = append(seqs, p.seq)
	}
	e.add(sigs, Signal{Detector: "S2", Rule: "s2.whack_a_mole", Confidence: 0.7, Level: L2, Evidence: seqs,
		Facts: map[string]any{"attempts": n, "failed": first}})
	st.failSeries = st.failSeries[len(st.failSeries)-1:]
}

// verifyAfterDocs raises s1.verify_after_docs when a verification repeats
// with the same result and only documentation changed since its last run:
// the change could not affect the outcome. It starts in shadow.
func (e *Engine) verifyAfterDocs(ev *event.Event, sigs *[]Signal) {
	st := e.St
	if runID(ev) == "" || e.nondeterministic(ev.CmdNorm) {
		return
	}
	prev, ok := st.lastRun[runID(ev)]
	st.lastRun[runID(ev)] = runMark{result: ev.ResultFP, ws: ev.WSBefore, seq: ev.Seq}
	if !ok || prev.result != ev.ResultFP || prev.ws == ev.WSBefore {
		return // a first run, a changed result, or an identical rerun (S1)
	}
	if st.lastWriteSeq > prev.seq && st.lastCodeWriteSeq < prev.seq {
		e.add(sigs, Signal{Detector: "S1", Rule: "s1.verify_after_docs", Confidence: 0.8, Level: L1, Evidence: []int64{prev.seq, ev.Seq},
			Facts: map[string]any{"cmd": ev.CmdNorm}})
	}
}

// IsReview reports a subagent call or a review command.
func IsReview(ev *event.Event) bool {
	if ev.Tool == event.ToolTask {
		return true
	}
	lower := strings.ToLower(ev.CmdNorm)
	return ev.Tool == event.ToolShell && (strings.Contains(lower, "codex exec") || strings.Contains(lower, " review") || strings.HasPrefix(lower, "review"))
}

// reviewRepeat raises s1.review_repeat when the same review or subagent
// purpose runs again on an unchanged workspace. A different purpose or a
// changed workspace is a different review. It starts in shadow.
func (e *Engine) reviewRepeat(ev *event.Event, sigs *[]Signal) {
	if ev.WSBefore == "" || !IsReview(ev) {
		return
	}
	purpose := ev.Purpose
	if purpose == "" {
		purpose = ev.CmdNorm
	}
	if purpose == "" {
		return
	}
	key := ev.WSBefore + "|" + purpose
	e.St.reviews[key]++
	if n := e.St.reviews[key]; n >= 2 {
		e.add(sigs, Signal{Detector: "S1", Rule: "s1.review_repeat", Confidence: 0.7, Level: L1, Evidence: []int64{ev.Seq},
			Facts: map[string]any{"purpose": purpose, "count": n, "cmd": purpose}})
	}
}

// runMark remembers the latest run of one command.
type runMark struct {
	result string
	ws     string
	seq    int64
}

// failMark remembers the latest failure of one command.
type failMark struct {
	class string
	ws    string
	seq   int64
}

// environment raises s2.environment when a failure that needs an action
// outside the code repeats without a workspace change: further code edits or
// reruns cannot fix it. Transient network failures get more attempts.
func (e *Engine) environment(ev *event.Event, sigs *[]Signal) {
	st := e.St
	if ev.Tool == event.ToolShell && ev.ExitCode != nil && *ev.ExitCode == 0 && runID(ev) != "" {
		// the environment recovered for this run: a later failure starts over
		for key := range st.envStreak {
			if strings.HasSuffix(key, "|"+runID(ev)) {
				delete(st.envStreak, key)
			}
		}
		delete(st.lastFail, runID(ev))
		for key := range st.recoveryUsed {
			if strings.HasSuffix(key, "|"+runID(ev)) {
				delete(st.recoveryUsed, key)
			}
		}
		return
	}
	if ev.Tool != event.ToolShell || ev.ExitCode == nil || *ev.ExitCode <= 0 || runID(ev) == "" {
		return
	}
	class := fp.ClassifyFailure(ev.Text)
	st.lastFail[runID(ev)] = failMark{class: class, ws: ev.WSBefore, seq: ev.Seq}
	need := 0
	switch {
	case fp.External(class):
		need = 2
	case class == fp.FailTransient:
		need = 4
	default:
		return
	}
	key := class + "|" + runID(ev)
	s := st.envStreak[key]
	if s == nil || s.lastWS != ev.WSBefore {
		s = &streak{lastWS: ev.WSBefore}
		st.envStreak[key] = s
	}
	s.count++
	s.seqs = append(s.seqs, ev.Seq)
	if s.count != need {
		return
	}
	lvl := L2
	if class == fp.FailTransient {
		lvl = L1
	}
	e.add(sigs, Signal{Detector: "S2", Rule: "s2.environment", Confidence: 0.85, Level: lvl, Evidence: append([]int64(nil), s.seqs...),
		Facts: map[string]any{"kind": class, "cmd": ev.CmdNorm, "count": s.count}})
}

func (e *Engine) verifyProgress(ev, prev *event.Event) {
	st := e.St
	improved := false
	if prev != nil && runID(prev) == runID(ev) && prev.ResultFP != ev.ResultFP {
		pf, cf := failing(prev), failing(ev)
		switch {
		case pf && !cf:
			improved = true
		case pf && cf && (len(ev.FailedTests) < len(prev.FailedTests) || (len(ev.FailedTests) == len(prev.FailedTests) && len(ev.ErrFPs) < len(prev.ErrFPs))):
			improved = true
		}
	} else if !failing(ev) && st.writes > 0 && (prev == nil || prev.Seq < st.lastWriteSeq) {
		// first passing verification after new writes
		improved = true
	}
	if improved {
		e.progress(ev)
	}
}

// progress records a progress event.
func (e *Engine) progress(ev *event.Event) {
	st := e.St
	st.ProgressSeqs[ev.Seq] = true
	st.LastProgress = ev.Seq
	clear(st.stuckCmd)
	st.ProgressMark = st.TotalMicro
	st.idleFired = false
	st.forced = 0
	st.forcedSeqs = nil
}

// CheckpointProgress is called by the live harness when a checkpoint meets a
// criterion.
func (e *Engine) CheckpointProgress(ev *event.Event) { e.progress(ev) }

func (e *Engine) inScope(rel string) (in bool, known bool) {
	if strings.HasPrefix(rel, "/tmp/") || strings.HasPrefix(rel, "/var/tmp/") || strings.HasPrefix(rel, ".samcheonpo/") {
		return true, true
	}
	if e.St.scopeKnown {
		if strings.HasPrefix(rel, "/") {
			return false, true
		}
		return e.Contract.InScope(rel), true
	}
	if len(e.St.allowGuess) == 0 {
		return true, false
	}
	if strings.HasPrefix(rel, "/") {
		return false, true
	}
	for _, g := range e.St.allowGuess {
		if g == rel {
			return true, true
		}
		pre := strings.TrimSuffix(g, "/**")
		if strings.HasPrefix(rel, pre+"/") || rel == pre {
			return true, true
		}
	}
	return false, true
}

func (e *Engine) production(ev *event.Event, sigs *[]Signal) {
	st := e.St
	st.writes++
	st.lastWriteSeq = ev.Seq
	for _, p := range ev.Paths {
		if !IsDocPath(p) {
			st.lastCodeWriteSeq = ev.Seq
		}
	}
	if !ev.TS.IsZero() {
		st.lastProduceTS = ev.TS
	}
	st.explore = nil
	for _, c := range ev.Created {
		st.created[c] = true
	}
	grew := false
	paths := ev.Paths
	for p, h := range ev.WriteHashes {
		_ = h
		if !contains(paths, p) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		st.readCount[p] = 0
		st.attemptFiles[p] = true
		add, rem := ev.AddedLines[p], ev.RemovedLines[p]
		if ev.Tool == event.ToolWrite && len(ev.Created) > 0 && contains(ev.Created, p) {
			st.lineCount[p] = add
		} else if ev.Tool == event.ToolWrite && rem == 0 && st.lineCount[p] == 0 {
			st.lineCount[p] = add
		} else {
			st.lineCount[p] += add - rem
		}
		if st.lineCount[p] > st.highWater[p] {
			if st.scopeKnown {
				if in, _ := e.inScope(p); in {
					grew = true
				}
			} else if !strings.HasPrefix(p, "/") {
				grew = true
			}
			st.highWater[p] = st.lineCount[p]
		}
		if classify.IsTestPath(p) {
			st.testAdded += add
		} else {
			st.codeAdded += add
		}
	}
	// Growth is progress only while no verification is failing; once a check
	// fails, only an improved verification result counts, so adding lines while
	// rerunning the same failing checks does not reset the idle budget.
	if grew {
		if st.lastVerify == nil || !failing(st.lastVerify) {
			e.progress(ev)
		} else {
			st.GrowthSeqs[ev.Seq] = true
		}
	}
	e.oscillation(ev, sigs)
	e.scope(ev, paths, sigs)
	e.bypass(ev, sigs)
	e.testBloat(ev, sigs)
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

func (e *Engine) oscillation(ev *event.Event, sigs *[]Signal) {
	st := e.St
	keys := make([]string, 0, len(ev.WriteHashes))
	for p := range ev.WriteHashes {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for _, p := range keys {
		h := ev.WriteHashes[p]
		hs := st.fileHist[p]
		if h != "" && !strings.HasPrefix(h, "edit:") && !strings.HasPrefix(h, "patch:") {
			for i := len(hs) - 2; i >= 0; i-- {
				if hs[i].hash == h && hs[len(hs)-1].hash != h {
					e.raiseOsc(ev, p, hs[i].seq, sigs)
					break
				}
			}
		}
		st.fileHist[p] = append(hs, hist{hash: h, seq: ev.Seq})
	}
	for _, ed := range ev.Edits {
		eh := st.editHist[ed.Path]
		for i := len(eh) - 1; i >= 0; i-- {
			if eh[i].new == ed.OldHash && eh[i].old == ed.NewHash && ed.OldHash != ed.NewHash {
				e.raiseOsc(ev, ed.Path, eh[i].seq, sigs)
				break
			}
		}
		st.editHist[ed.Path] = append(eh, editHist{old: ed.OldHash, new: ed.NewHash, seq: ev.Seq})
	}
}

func (e *Engine) raiseOsc(ev *event.Event, p string, back int64, sigs *[]Signal) {
	st := e.St
	st.reinserts++
	// writes to p after the restored state up to and including this one are reverts
	var rng []int64
	for _, x := range st.Events {
		if x.Seq > back && x.Seq <= ev.Seq && x.Category == event.CatProduce && (contains(x.Paths, p) || x.WriteHashes[p] != "") {
			rng = append(rng, x.Seq)
		}
	}
	e.add(sigs, Signal{Detector: "S2", Rule: "s2.oscillation", Confidence: 0.9, Level: L1, Evidence: append([]int64{back}, rng...),
		Facts: map[string]any{"path": p, "back_to_seq": back, "minutes": minutesBetween(st.bySeq[back], ev)}})
}

func minutesBetween(a, b *event.Event) int {
	if a == nil || b == nil || a.TS.IsZero() || b.TS.IsZero() {
		return 0
	}
	return int(b.TS.Sub(a.TS).Minutes())
}

func (e *Engine) scope(ev *event.Event, paths []string, sigs *[]Signal) {
	st := e.St
	for _, p := range paths {
		if p == "" {
			continue
		}
		in, known := e.inScope(p)
		if known && !in && !st.scopeKnown {
			e.guessedOut(ev, p, sigs)
			continue
		}
		if !known || in || st.outScope[p] {
			continue
		}
		st.outScope[p] = true
		n := len(st.outScope)
		conf := 0.85
		est := false
		if !st.scopeKnown {
			conf, est = 0.4, true
		}
		lvl := L1
		if n >= e.Cfg.Detectors.S3.NotifyFiles {
			lvl = L2
		}
		if est {
			st.Estimated[ev.Seq] = true
		}
		if n == 1 || n == e.Cfg.Detectors.S3.NotifyFiles || st.scopeKnown {
			e.add(sigs, Signal{Detector: "S3", Rule: "s3.out_of_scope", Confidence: conf, Level: lvl, Evidence: []int64{ev.Seq}, Estimate: est,
				Facts: map[string]any{"path": p, "count": n}})
		}
	}
	for _, p := range paths {
		if classify.IsManifest(p) || classify.IsToolConfig(p) {
			kind := "config"
			if classify.IsManifest(p) {
				kind = "dependency"
			}
			if e.Contract != nil && kind == "dependency" && !forbids(e.Contract, "의존성", "dependenc", "라이브러리") {
				continue
			}
			e.add(sigs, Signal{Detector: "S3", Rule: "s3.config_bypass", Confidence: 0.6, Level: L1, Evidence: []int64{ev.Seq},
				Facts: map[string]any{"path": p, "kind": kind}})
		}
	}
}

// guessedOut handles a write outside a scope guessed from the request (no
// accepted contract). One write is noise; the second write into the same
// directory group is advised, never blocked. A write back into the previous
// revision's scope after a compaction is reported as a return to an old goal.
func (e *Engine) guessedOut(ev *event.Event, p string, sigs *[]Signal) {
	st := e.St
	if IsExploreTask(e.FirstPrompt) {
		return
	}
	st.Estimated[ev.Seq] = true
	if st.compacted && inGuess(st.prevGuess, p) {
		e.add(sigs, Signal{Detector: "S3", Rule: "s3.out_of_scope", Confidence: 0.6, Level: L1, Evidence: []int64{ev.Seq}, Estimate: true,
			Facts: map[string]any{"path": p, "kind": "stale_goal", "count": 1}})
		return
	}
	g := path.Dir(p)
	st.guessOut[g]++
	switch st.guessOut[g] {
	case 1: // recorded as an estimate for receipts, not delivered
		e.add(sigs, Signal{Detector: "S3", Rule: "s3.out_of_scope", Confidence: 0.4, Level: L0, Evidence: []int64{ev.Seq}, Estimate: true,
			Facts: map[string]any{"path": p, "count": 1, "kind": "guessed"}})
	case 2:
		e.add(sigs, Signal{Detector: "S3", Rule: "s3.out_of_scope", Confidence: 0.55, Level: L1, Evidence: []int64{ev.Seq}, Estimate: true,
			Facts: map[string]any{"path": p, "count": 2, "kind": "guessed"}})
	}
}

// workScope is the scope guessed from a request for detection. Test paths
// are dropped: "make tests/x pass" is work on the code under test, so a
// request that names only tests gives no usable scope.
func workScope(prompt string) []string {
	var out []string
	for _, g := range GuessScope(prompt) {
		if !classify.IsTestPath(strings.TrimSuffix(g, "/**")) && !classify.IsTestPath(strings.TrimSuffix(g, "/**")+"/x") {
			out = append(out, g)
		}
	}
	return out
}

func inGuess(globs []string, rel string) bool {
	for _, g := range globs {
		pre := strings.TrimSuffix(g, "/**")
		if g == rel || rel == pre || strings.HasPrefix(rel, pre+"/") {
			return true
		}
	}
	return false
}

// InScope reports whether a path is inside the accepted contract scope or,
// without one, the scope guessed from the request; known is false when there
// is no scope to compare with.
func (e *Engine) InScope(rel string) (in, known bool) { return e.inScope(rel) }

// Retarget applies a new intent revision's goal: the guessed scope and its
// counters restart, and the old scope is kept to spot a return after a
// compaction. An accepted contract scope is not touched.
func (e *Engine) Retarget(goal string) {
	e.FirstPrompt = goal
	st := e.St
	if st.scopeKnown {
		return
	}
	st.prevGuess = st.allowGuess
	st.allowGuess = workScope(goal)
	st.guessOut = map[string]int{}
	st.compacted = false
}

func forbids(c *contract.Contract, words ...string) bool {
	for _, f := range c.Forbid {
		l := strings.ToLower(f)
		for _, w := range words {
			if strings.Contains(l, w) {
				return true
			}
		}
	}
	return false
}

func (e *Engine) bypass(ev *event.Event, sigs *[]Signal) {
	st := e.St
	for _, pf := range ev.Patch {
		p := pf.Path
		isTest := classify.IsTestPath(p)
		if isTest && classify.TestInfraNames[path.Base(p)] {
			e.add(sigs, Signal{Detector: "S5", Rule: "s5.test_weakening", Confidence: 0.7, Level: L1, Evidence: []int64{ev.Seq},
				Facts: map[string]any{"path": p, "kind": string(KindTestInfraChanged)}})
		}
		if isTest {
			if pf.Deleted && !st.created[p] {
				e.add(sigs, Signal{Detector: "S5", Rule: "s5.test_weakening", Confidence: 0.9, Level: L2, Evidence: []int64{ev.Seq},
					Facts: map[string]any{"path": p, "kind": string(KindTestDeleted)}})
				continue
			}
			// a test file created in this session is the agent's own work, not a trusted test
			if st.created[p] || contains(ev.Created, p) {
				for _, l := range ExpectedLiterals(pf.Added) {
					if _, ok := st.testLits[l]; !ok {
						st.testLits[l] = ev.Seq
					}
				}
				continue
			}
			if MockSubstitution(pf.Added, pf.Removed) && !MentionsMock(e.FirstPrompt) {
				e.add(sigs, Signal{Detector: "S5", Rule: "s5.test_weakening", Confidence: 0.75, Level: L1, Evidence: []int64{ev.Seq},
					Facts: map[string]any{"path": p, "kind": string(KindMockSubstituted)}})
			}
			for _, k := range TestChange(pf.Added, pf.Removed) {
				conf, lvl := 0.9, L2
				if k == KindLiteralReplaced {
					// expected values also change when the specification changes
					conf, lvl = 0.6, L1
				}
				e.add(sigs, Signal{Detector: "S5", Rule: "s5.test_weakening", Confidence: conf, Level: lvl, Evidence: []int64{ev.Seq},
					Facts: map[string]any{"path": p, "kind": string(k)}})
			}
			for _, l := range ExpectedLiterals(pf.Added) {
				if _, ok := st.testLits[l]; !ok {
					st.testLits[l] = ev.Seq
				}
			}
			continue
		}
		for _, k := range HidingChange(pf.Added, pf.Removed) {
			e.add(sigs, Signal{Detector: "S5", Rule: "s5.error_hiding", Confidence: 0.8, Level: L1, Evidence: []int64{ev.Seq},
				Facts: map[string]any{"path": p, "kind": string(k)}})
		}
		for _, a := range pf.Added {
			for l, seq := range st.testLits {
				if strings.Contains(a, l) {
					e.add(sigs, Signal{Detector: "S5", Rule: "s5.answer_copy", Confidence: 0.45, Level: L2, Evidence: []int64{seq, ev.Seq},
						Facts: map[string]any{"path": p, "literal_len": len(l)}})
					delete(st.testLits, l)
					break
				}
			}
		}
	}
	// test files removed through the shell; removing the feature's source in
	// the same command is a feature removal, not test weakening
	if ev.Tool == event.ToolShell {
		featureRemoval := false
		for _, p := range ev.Deleted {
			if !classify.IsTestPath(p) {
				featureRemoval = true
			}
		}
		for _, p := range ev.Deleted {
			if classify.IsTestPath(p) && !st.created[p] {
				conf := 0.9
				if featureRemoval {
					conf = 0.45
				}
				e.add(sigs, Signal{Detector: "S5", Rule: "s5.test_weakening", Confidence: conf, Level: L2, Evidence: []int64{ev.Seq},
					Facts: map[string]any{"path": p, "kind": string(KindTestDeleted), "feature_removal": featureRemoval}})
			}
		}
	}
}

func (e *Engine) testBloat(ev *event.Event, sigs *[]Signal) {
	st := e.St
	if st.bloatFired || st.testAdded < 60 || float64(st.testAdded) <= float64(st.codeAdded)*e.Cfg.Detectors.S1.TestBloat {
		return
	}
	if e.Contract.AllowsTestWriting() || strings.Contains(strings.ToLower(e.FirstPrompt), "test") || strings.Contains(e.FirstPrompt, "테스트") || strings.Contains(e.FirstPrompt, "시험") {
		return
	}
	st.bloatFired = true
	e.add(sigs, Signal{Detector: "S1", Rule: "s1.test_bloat", Confidence: 0.6, Level: L0, Evidence: []int64{ev.Seq},
		Facts: map[string]any{"test_lines": st.testAdded, "code_lines": st.codeAdded}})
}

func (e *Engine) exploration(ev *event.Event, sigs *[]Signal) {
	st := e.St
	st.explore = append(st.explore, ev.Seq)
	for _, p := range ev.Paths {
		if ev.Tool != event.ToolRead && !(ev.Tool == event.ToolShell && p != "") {
			continue
		}
		st.reads++
		st.readCount[p]++
		if st.readCount[p] >= e.Cfg.Detectors.S4.RereadCount {
			st.rereads++
		}
	}
	if (e.Contract != nil && e.Contract.Explore) || (e.Contract == nil && IsExploreTask(e.FirstPrompt)) {
		return
	}
	long := false
	if !ev.TS.IsZero() && !st.lastProduceTS.IsZero() && len(st.explore) >= 5 {
		long = ev.TS.Sub(st.lastProduceTS) >= time.Duration(e.Cfg.Detectors.S4.MaxMinutes)*time.Minute
	}
	if len(st.explore) >= e.threshold("s4.read_only_streak", e.Cfg.Detectors.S4.MaxReads) || long {
		e.add(sigs, Signal{Detector: "S4", Rule: "s4.read_only_streak", Confidence: 0.75, Level: L1, Evidence: append([]int64(nil), st.explore...),
			Facts: map[string]any{"count": len(st.explore), "minutes": minutesSince(st.lastProduceTS, ev.TS)}})
		st.explore = nil
		st.lastProduceTS = ev.TS
	}
}

var exploreTaskRe = lazyre.New(`(?i)(검토|리뷰|분석|조사|파악|설명|알려|찾아|확인해|읽어|요약|비교|평가|진단|살펴|\breview\b|\banaly[sz]e|\binvestigate|\bexplain|\bsummar|\baudit\b|\bresearch|\bwhy\b|\bwhat\b|\bhow does)`)
var buildTaskRe = lazyre.New(`(?i)(고쳐|수정해|만들어|구현|추가해|작성해|바꿔|변경해|리팩터|refactor|\bfix\b|\bimplement|\badd\b|\bcreate\b|\bbuild\b|\bwrite\b|\bchange\b)`)

var docExt = map[string]bool{".md": true, ".txt": true, ".rst": true, ".adoc": true, ".org": true}

// IsDocPath reports documentation files (writing them does not invalidate a verification).
func IsDocPath(p string) bool {
	return docExt[strings.ToLower(path.Ext(p))] || strings.HasPrefix(p, "docs/") || strings.Contains(p, "/docs/")
}

// IsExploreTask reports whether a request is primarily reading (review,
// analysis, explanation), where long read-only stretches are the work itself.
func IsExploreTask(prompt string) bool {
	p := prompt
	if len(p) > 600 {
		p = p[:600]
	}
	return exploreTaskRe.MatchString(p) && !buildTaskRe.MatchString(p)
}

func minutesSince(a, b time.Time) int {
	if a.IsZero() || b.IsZero() {
		return 0
	}
	return int(b.Sub(a).Minutes())
}

func (e *Engine) verifyRatio(ev *event.Event, sigs *[]Signal) {
	st := e.St
	w := e.Cfg.Detectors.S1.RatioWindow
	if len(st.toolWin) < w {
		return
	}
	v, p := 0, 0
	for _, x := range st.toolWin {
		switch x.Category {
		case event.CatVerify:
			v++
		case event.CatProduce:
			p++
		}
		if st.ProgressSeqs[x.Seq] {
			return
		}
	}
	if float64(v) >= e.Cfg.Detectors.S1.Ratio*float64(max(p, 1)) {
		var seqs []int64
		for _, x := range st.toolWin {
			seqs = append(seqs, x.Seq)
		}
		e.add(sigs, Signal{Detector: "S1", Rule: "s1.verify_ratio", Confidence: 0.7, Level: L1, Evidence: seqs,
			Facts: map[string]any{"verify": v, "produce": p, "window": w}})
		st.toolWin = nil
	}
}

func (e *Engine) memoryRot(ev *event.Event, sigs *[]Signal) {
	st := e.St
	if st.rotFired {
		return
	}
	conds := 0
	if st.compacts >= e.Cfg.Detectors.S7.Compactions {
		conds++
	}
	if st.reads >= 10 && st.rereads >= 3 && float64(st.rereads)/float64(st.reads) >= 0.25 {
		conds++
	}
	if st.reinserts >= 1 {
		conds++
	}
	if conds >= 2 {
		st.rotFired = true
		e.add(sigs, Signal{Detector: "S7", Rule: "s7.memory_rot", Confidence: 0.7, Level: L4, Evidence: []int64{ev.Seq},
			Facts: map[string]any{"compactions": st.compacts, "rereads": st.rereads, "reads": st.reads, "reinserts": st.reinserts}})
	}
}

func (e *Engine) forcedTurn(ev *event.Event, sigs *[]Signal) {
	st := e.St
	st.forced++
	st.forcedSeqs = append(st.forcedSeqs, ev.Seq)
	if st.forced == 3 {
		e.add(sigs, Signal{Detector: "S8", Rule: "s8.forced_no_progress", Confidence: 0.85, Level: L2, Evidence: append([]int64(nil), st.forcedSeqs...),
			Facts: map[string]any{"count": st.forced}})
	}
}

func (e *Engine) cost(ev *event.Event, sigs *[]Signal) {
	st := e.St
	d := e.Cfg.Detectors.S8
	budget := int64(0)
	if e.Contract != nil {
		budget = e.Contract.Budget.KRW * 1_000_000
	}
	// budget
	if budget > 0 {
		for _, pct := range []int{80, 100} {
			if !st.budgetFired[pct] && st.TotalMicro*100 >= budget*int64(pct) {
				st.budgetFired[pct] = true
				lvl := L2
				if pct == 100 {
					lvl = L3
				}
				e.add(sigs, Signal{Detector: "S8", Rule: "s8.budget", Confidence: 1.0, Level: lvl, Evidence: []int64{ev.Seq},
					Facts: map[string]any{"pct": pct, "spent_krw": st.TotalMicro / 1_000_000, "budget_krw": budget / 1_000_000}})
			}
		}
	}
	// idle spend
	idle := st.TotalMicro - st.ProgressMark
	limit := d.IdleSpendFloorKRW * 1_000_000
	if b := int64(float64(budget) * d.IdleSpendBudgetRatio); b > limit {
		limit = b
	}
	quiet := 0
	for i := len(st.Events) - 1; i >= 0 && st.Events[i].Seq > st.LastProgress; i-- {
		if st.Events[i].Kind == event.KindTool {
			quiet++
		}
	}
	exploreTask := (e.Contract != nil && e.Contract.Explore) || (e.Contract == nil && IsExploreTask(e.FirstPrompt))
	if !st.idleFired && idle > limit && quiet >= d.IdleMinToolEvents && !exploreTask {
		st.idleFired = true
		e.add(sigs, Signal{Detector: "S8", Rule: "s8.idle_spend", Confidence: 0.8, Level: L2, Evidence: []int64{ev.Seq},
			Facts: map[string]any{"idle_krw": idle / 1_000_000, "since_seq": st.LastProgress}})
	}
	// velocity
	if ev.TS.IsZero() || st.sessionStart.IsZero() || ev.TS.Sub(st.sessionStart) < 15*time.Minute {
		return
	}
	nowMin := ev.TS.Unix() / 60
	var win int64
	for m := nowMin - 4; m <= nowMin; m++ {
		win += st.minute[m]
	}
	perMin := win / 5
	var vals []int64
	for m, v := range st.minute {
		if m < nowMin-4 && v > 0 {
			vals = append(vals, v)
		}
	}
	if len(vals) < 5 {
		return
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	med := vals[len(vals)/2]
	bucket := nowMin / 5
	if float64(perMin) >= float64(med)*d.VelocityMultiplier && perMin > d.VelocityFloorKRWPerMin*1_000_000 && !st.velocityFired[bucket] && !st.velocityFired[bucket-1] {
		st.velocityFired[bucket] = true
		e.add(sigs, Signal{Detector: "S8", Rule: "s8.velocity", Confidence: 0.8, Level: L2, Evidence: []int64{ev.Seq},
			Facts: map[string]any{"per_min_krw": perMin / 1_000_000, "median_krw": med / 1_000_000}})
	}
}

// TurnEnd evaluates a turn's final assistant message (false completion).
// unmet reports whether a live checkpoint of an accepted contract failed.
func (e *Engine) TurnEnd(msg *event.Event, unmet []string) []Signal {
	st := e.St
	var sigs []Signal
	if msg == nil || !IsCompletionClaim(msg.Text) || st.writes == 0 {
		return nil
	}
	lv := st.lastVerify
	switch {
	case len(unmet) > 0:
		e.add(&sigs, Signal{Detector: "S5", Rule: "s5.false_done", Confidence: 0.8, Level: L2, Evidence: []int64{msg.Seq},
			Facts: map[string]any{"kind": "unmet", "unmet": unmet}})
	case lv == nil:
		e.add(&sigs, Signal{Detector: "S5", Rule: "s5.false_done", Confidence: 0.6, Level: L0, Evidence: []int64{msg.Seq},
			Facts: map[string]any{"kind": "unverified"}})
	case failing(lv):
		e.add(&sigs, Signal{Detector: "S5", Rule: "s5.false_done", Confidence: 0.8, Level: L2, Evidence: []int64{lv.Seq, msg.Seq},
			Facts: map[string]any{"kind": "failed", "cmd": lv.CmdNorm, "failed_tests": lv.FailedTests}})
	case lv.Seq < st.lastCodeWriteSeq:
		e.add(&sigs, Signal{Detector: "S5", Rule: "s5.false_done", Confidence: 0.7, Level: L1, Evidence: []int64{lv.Seq, st.lastWriteSeq, msg.Seq},
			Facts: map[string]any{"kind": "stale", "cmd": lv.CmdNorm}})
	}
	return e.combine(msg, sigs)
}

// PreCheck is the live PreToolUse decision: it returns a deny reason when the
// call is an identical verification rerun or touches a protected path.
func (e *Engine) PreCheck(ev *event.Event) (deny bool, sig *Signal) {
	st := e.St
	if ev.Tool == event.ToolShell && ev.Category == event.CatVerify && runID(ev) != "" && blockableRun(ev) && !e.nondeterministic(ev.CmdNorm) {
		key := runID(ev) + "|" + ev.WSBefore
		if ent := st.verify[key]; ent != nil && ent.count >= e.threshold("s1.identical_rerun", e.Cfg.Detectors.S1.RepeatNudgeAt)-1 && !e.recoveryCheck(ev) {
			last := st.bySeq[ent.seqs[len(ent.seqs)-1]]
			s := Signal{Detector: "S1", Rule: "s1.identical_rerun", Confidence: 0.95, Level: L1, Evidence: append([]int64(nil), ent.seqs...),
				Facts: map[string]any{"cmd": ev.CmdNorm, "count": ent.count + 1, "blocked": true}}
			if last != nil {
				s.Facts["failed_tests"] = last.FailedTests
				if last.ExitCode != nil {
					s.Facts["exit"] = *last.ExitCode
				}
			}
			return true, &s
		}
	}
	// S2: rerunning a command that keeps failing the same way, with no change
	// since its last failure. Failures that need an outside action are left
	// alone: the environment may have changed where no fingerprint can see.
	if ev.Tool == event.ToolShell && ev.Category == event.CatVerify && runID(ev) != "" && blockableRun(ev) && ev.WSBefore != "" && st.stuckCmd[runID(ev)] && !e.nondeterministic(ev.CmdNorm) {
		if lf, ok := st.lastFail[runID(ev)]; ok && lf.ws == ev.WSBefore && !fp.External(lf.class) && lf.class != fp.FailTransient {
			s := Signal{Detector: "S2", Rule: "s2.stuck_error", Confidence: 0.9, Level: L1, Evidence: []int64{lf.seq},
				Facts: map[string]any{"cmd": ev.CmdNorm, "count": 1, "blocked": true, "unchanged": true}}
			return true, &s
		}
	}
	// S5: writing an error-hiding pattern again into a file where the same
	// pattern was already raised.
	if ev.Category == event.CatProduce && (ev.Tool == event.ToolWrite || ev.Tool == event.ToolEdit) {
		for _, pf := range ev.Patch {
			if classify.IsTestPath(pf.Path) {
				continue
			}
			for _, k := range HidingChange(pf.Added, pf.Removed) {
				if st.hiding[pf.Path][string(k)] {
					s := Signal{Detector: "S5", Rule: "s5.error_hiding", Confidence: 0.8, Level: L1,
						Facts: map[string]any{"path": pf.Path, "kind": string(k), "blocked": true}}
					return true, &s
				}
			}
		}
	}
	if e.Contract != nil && e.Accepted {
		for _, p := range ev.Paths {
			if e.Contract.Protected(p) || (classify.IsTestPath(p) && e.Contract.ForbidsTestChanges() && (ev.Category == event.CatProduce)) {
				if ev.Category != event.CatProduce && ev.Tool != event.ToolWrite && ev.Tool != event.ToolEdit {
					continue
				}
				s := Signal{Detector: "S3", Rule: "s3.protected_path", Confidence: 1.0, Level: L3, Facts: map[string]any{"path": p}}
				return true, &s
			}
		}
	}
	return false, nil
}

// recoveryCheck lets one rerun through after the last failure of this run was
// caused outside the code (service down, missing dependency, permission,
// network): the workspace fingerprint cannot see a service coming back, so
// an unchanged fingerprint is no evidence that nothing changed. The check is
// granted once per run and cause; a success resets it, another failure from
// the same cause ends it.
func (e *Engine) recoveryCheck(ev *event.Event) bool {
	st := e.St
	lf, ok := st.lastFail[runID(ev)]
	if !ok || !(fp.External(lf.class) || lf.class == fp.FailTransient) {
		return false
	}
	key := lf.class + "|" + runID(ev)
	if st.recoveryUsed[key] {
		return false
	}
	st.recoveryUsed[key] = true
	return true
}

// threshold returns a configured threshold raised by per-rule overrides.
func (e *Engine) threshold(rule string, base int) int {
	if n, ok := e.Cfg.Detectors.Overrides[rule]; ok && n > 0 {
		// each override step adds 50% of the base, capped at 2x
		v := base + (base*n+1)/2
		if v > base*2 {
			v = base * 2
		}
		return v
	}
	return base
}

// markWaste assigns not-yet-wasted events to a symptom and returns their cost.
func (e *Engine) markWaste(seqs []int64, symptom string) int64 {
	var sum int64
	for _, s := range seqs {
		if _, ok := e.St.Wasted[s]; ok {
			continue
		}
		ev := e.St.bySeq[s]
		if ev == nil {
			continue
		}
		e.St.Wasted[s] = symptom
		e.St.invalidateUsage(ev)
		sum += ev.CostMicroKRW
	}
	return sum
}

// combine applies confidence floor, cooldown and escalation.
func (e *Engine) combine(ev *event.Event, sigs []Signal) []Signal {
	if len(sigs) == 0 {
		return nil
	}
	cool := int64(e.Cfg.Levels.NudgeCooldownEvents)
	best := -1
	for i := range sigs {
		s := &sigs[i]
		s.Seq = ev.Seq
		e.nextID++
		s.ID = fmt.Sprintf("%s-%d", e.Session, e.nextID)
		if s.Confidence < 0.5 {
			s.Level = L0
		}
		if s.Level == L0 {
			continue
		}
		if s.Rule == "s5.error_hiding" {
			if p, _ := s.Facts["path"].(string); p != "" {
				if k, _ := s.Facts["kind"].(string); k != "" {
					if e.St.hiding[p] == nil {
						e.St.hiding[p] = map[string]bool{}
					}
					e.St.hiding[p][k] = true
				}
			}
		}
		// a stronger signal than the one that started the cooldown is new information
		if until, ok := e.cooldownUntil[s.Rule]; ok && ev.Seq <= until && s.Level <= e.cooldownLevel[s.Rule] {
			s.Suppressed = true
			if e.escalate[s.Rule] < 1 {
				e.escalate[s.Rule] = 1
			}
			continue
		}
		if b := e.escalate[s.Rule]; b > 0 {
			s.Level += b
			if s.Level > L4 {
				s.Level = L4
			}
			delete(e.escalate, s.Rule)
		}
		if best < 0 || s.Level > sigs[best].Level {
			best = i
		}
	}
	if best >= 0 {
		sigs[best].Primary = true
		e.cooldownUntil[sigs[best].Rule] = ev.Seq + cool
		e.cooldownLevel[sigs[best].Rule] = sigs[best].Level
		if sigs[best].Level == L1 && e.Mode == "live" && e.Cfg.Experiment.Enabled && sigs[best].Detector != "S5" {
			sigs[best].Arm = Arm(e.Session, sigs[best].Rule)
		}
	}
	for _, s := range sigs {
		if s.Estimate {
			for _, sq := range s.Evidence {
				e.St.Estimated[sq] = true
			}
		}
	}
	e.Verdicts = append(e.Verdicts, sigs...)
	return sigs
}

// Arm deterministically assigns an experiment arm.
func Arm(session, rule string) string {
	h := fp.Hash("arm", session, rule)
	switch h[0] % 3 {
	case 0:
		return "none"
	case 1:
		return "fact"
	default:
		return "prescription"
	}
}

// AddCost adds cost that arrived after an event was observed (live usage).
func (e *Engine) AddCost(ev *event.Event, micro int64) {
	// Unpriced token updates can have zero monetary delta.
	e.St.invalidateUsage(ev)
	if micro == 0 {
		return
	}
	e.St.TotalMicro += micro
	if !ev.TS.IsZero() {
		e.St.minute[ev.TS.Unix()/60] += micro
	}
}

// ScopeChanged re-reads scope knowledge after a contract was accepted.
func (e *Engine) ScopeChanged() {
	e.St.scopeKnown = e.Contract != nil && len(e.Contract.Scope.Allow) > 0
}

// LastVerifyFailing reports whether the most recent verification failed.
func (s *State) LastVerifyFailing() bool { return s.lastVerify != nil && failing(s.lastVerify) }

// Produced reports whether the session wrote anything.
func (s *State) Produced() bool { return s.writes > 0 }

// Emit runs externally raised signals (live stop blocks) through the same
// combination, cooldown and recording as detector signals.
func (e *Engine) Emit(ev *event.Event, sigs []Signal) []Signal { return e.combine(ev, sigs) }

// ObserveWatch records a zero-cost harness event (checkpoint) in order.
func (e *Engine) ObserveWatch(ev *event.Event) {
	e.St.Events = append(e.St.Events, ev)
	e.St.bySeq[ev.Seq] = ev
}

// capabilityLimit (S6): an attempt is the set of files written since the
// previous failing verification. Three attempts with different file sets
// that all fail within the same error family mean the approaches the agent
// reaches are exhausted; the prescriptions are a stronger model, splitting
// the task, or a question for the user.
func (e *Engine) capabilityLimit(ev *event.Event, family []string, sigs *[]Signal) {
	st := e.St
	if len(st.attemptFiles) == 0 {
		return
	}
	files := make([]string, 0, len(st.attemptFiles))
	for f := range st.attemptFiles {
		files = append(files, f)
	}
	sort.Strings(files)
	st.attemptFiles = map[string]bool{}
	fam := map[string]bool{}
	for _, f := range family {
		fam[f] = true
	}
	key := strings.Join(files, "\x00")
	// keep only strategies sharing this failure's family
	var keep []strategy
	for _, s := range st.strategies {
		for f := range s.family {
			if fam[f] {
				keep = append(keep, s)
				break
			}
		}
	}
	dup := false
	var same *strategy
	for i := range keep {
		if keep[i].files == key {
			dup = true
			s := &keep[i]
			if s.failed > 0 && len(ev.FailedTests) < s.failed {
				s.count = 0 // fewer failures: the same files are still making progress
			}
			s.count++
			s.failed = len(ev.FailedTests)
			s.seq = ev.Seq
			same = s
		}
	}
	if !dup {
		keep = append(keep, strategy{files: key, family: fam, seq: ev.Seq, count: 1, failed: len(ev.FailedTests)})
	}
	st.strategies = keep
	if same != nil && same.count == sameFilesAttempts && !st.s6SameFired {
		st.s6SameFired = true
		e.add(sigs, Signal{Detector: "S6", Rule: "s6.capability_limit", Confidence: 0.7, Level: L2, Evidence: []int64{ev.Seq},
			Facts: map[string]any{"same_files": true, "files": strings.ReplaceAll(key, "\x00", ", "), "attempts": same.count, "error": errorLabel(ev), "options": []string{"model", "split", "question"}}})
	}
	if st.s6Fired || len(keep) < 3 {
		return
	}
	st.s6Fired = true
	var seqs []int64
	for _, s := range keep {
		seqs = append(seqs, s.seq)
	}
	e.add(sigs, Signal{Detector: "S6", Rule: "s6.capability_limit", Confidence: 0.7, Level: L4, Evidence: seqs,
		Facts: map[string]any{"strategies": len(keep), "error": errorLabel(ev), "options": []string{"model", "split", "question"}}})
}
