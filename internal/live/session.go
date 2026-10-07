// Package live implements the real-time harness: the daemon, per-session
// state, hook handling, workspace fingerprints and the checkpoint runner.
package live

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/claude"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/codex"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intervene"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/judge"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/notify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/patch"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/policy"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/receipt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rules"
)

// Session is the live state of one agent session.
type Session struct {
	task                        *intent.Task
	intentRevision              *intent.Revision
	taskLink                    *intent.SessionLink
	recoveries                  []recovery.Attempt
	ID, Agent, Root, Transcript string
	Cfg                         config.Config
	CfgHash                     string

	mu               sync.Mutex
	parser           *claude.Parser
	usage            *claude.UsageTracker
	eng              *detect.Engine
	prices           *cost.Table
	db               *ledger.DB
	ws               *Workspace
	runner           *Runner
	c                *contract.Contract
	acc              contract.Acceptance
	contractRevision uint64
	checkMu          sync.Mutex
	opMu             sync.RWMutex
	finalizeMu       sync.Mutex
	queueMu          sync.Mutex
	queueClosed      bool
	queueRejected    atomic.Uint64
	// preLate counts pre-tool decisions that could not finish inside the
	// hook client's deadline; such a block may never have reached the agent.
	preLate         atomic.Uint64
	workerDone      chan struct{}
	storageErr      error
	finalized       bool
	receiptPath     string
	queue           chan *job
	byTool          map[string]*event.Event
	msgEvents       map[string]*event.Event // message id -> event holding tool-less usage
	caps            adapter.Caps
	judge           judge.Judge
	judging         bool
	drifts          int // consecutive drift judgements
	toolsSinceJudge int
	cx              *codex.Tracker
	sinceTokens     []*event.Event            // calls since the last usage record (codex, plugins)
	usageTargets    map[string][]*event.Event // plugin message id -> calls its usage belongs to
	usageSeen       map[string]event.Usage    // plugin message id -> usage already attributed
	awaiting        map[string]*event.Event   // pre-tool events still waiting for a result
	flushed         map[string]bool           // calls closed at a turn boundary without a result
	shell           map[string]bool           // running shell tool ids
	pending         []string                  // nudges waiting for the next hook response
	pendingDraft    string                    // latest draft awaiting capability confirmation
	anchorDue       bool                      // a compaction happened; the next readable response repeats the contract anchor
	userMsg         []string                  // user messages waiting (systemMessage)
	stopBlocks      int
	// advised holds escalation keys (revision, rule, pattern, target) whose
	// advice reached the agent; released holds rules the user marked as false
	// positives. Both drive policy.Escalate.
	advised     map[string]bool
	released    map[string]bool
	escalations int
	pendingRule map[string]string // queued agent message -> escalation key
	// pendingEdits are edits since the last verification; failedEdits are
	// edits that already failed in the current task revision (any session).
	pendingEdits      []pendingEdit
	failedEdits       map[string]ledger.FailedAttempt
	produceSinceCheck int
	lastPrimary       *detect.Signal
	unresolved        *detect.Signal
	rb                rollbackState
	golden            *patch.GoldenStateManager
	checks            map[string]CheckResult
	firstPrompt       string
	prompts           int
	lastActive        time.Time
	notifier          *notify.Notifier
	hud               *notify.HUDManager
	daemon            *Daemon // nil outside a daemon (tests, audit)
	swarmReported     int64   // microKRW already reported to the swarm registry
	closed            bool
	watchCost         int64
	judgeBudget       judge.Budget
	judgeBudgetReason string
	judgeCancel       context.CancelFunc
}

type job struct {
	ev   *event.Event
	done chan []detect.Signal
}

func (s *Session) activeAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActive
}

func newSession(id, agent, root, transcript string, db *ledger.DB, prices *cost.Table, caps adapter.Caps) (*Session, error) {
	if id == "" || id == "." || id == ".." || len(id) > 240 || strings.ContainsAny(id, "/\\\x00\r\n") {
		return nil, fmt.Errorf("안전하지 않은 세션 ID")
	}
	var err error
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if db != nil {
		if err := db.CheckSessionAgent(id, agent); err != nil {
			return nil, err
		}
	}
	cfg, _, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	s := &Session{ID: id, Agent: agent, Root: root, Transcript: transcript, Cfg: cfg, CfgHash: config.Hash(cfg), db: db, prices: prices, caps: caps,
		byTool: map[string]*event.Event{}, msgEvents: map[string]*event.Event{}, usageTargets: map[string][]*event.Event{}, usageSeen: map[string]event.Usage{}, awaiting: map[string]*event.Event{}, flushed: map[string]bool{}, shell: map[string]bool{}, checks: map[string]CheckResult{}, advised: map[string]bool{}, failedEdits: map[string]ledger.FailedAttempt{}, pendingRule: map[string]string{}, released: map[string]bool{}, queue: make(chan *job, 256), workerDone: make(chan struct{}), lastActive: time.Now()}
	s.applyOverrides()
	s.parser = claude.NewParser(transcript)
	s.parser.Session.ID = id
	s.parser.Session.Agent = agent
	s.parser.Session.Mode = "live"
	s.parser.SetRoot(root)
	if transcript != "" {
		switch agent {
		case "codex":
			s.cx = codex.NewTracker(transcript)
		default:
			s.usage = claude.NewUsageTracker(transcript)
			// resumed sessions: start at the current end; earlier usage belongs to earlier runs
			if st, err := os.Stat(transcript); err == nil {
				s.usage.Offset = st.Size()
			}
		}
	}
	s.ws = NewWorkspace(root)
	s.runner = &Runner{Root: root, WS: s.ws, Timeout: time.Duration(cfg.Checkpoint.TimeoutSec) * time.Second, Busy: s.shellBusy}
	s.loadContract()
	if err := s.restoreIntent(); err != nil {
		s.ws.Close()
		return nil, err
	}
	if err := s.restoreJudgeBudget(); err != nil {
		s.ws.Close()
		return nil, err
	}
	if db != nil {
		gap, err := db.ObservationGap(agent, id)
		if err != nil {
			s.ws.Close()
			return nil, err
		}
		s.queueRejected.Store(gap)
		s.recoveries, err = db.Recoveries(agent, id)
		if err != nil {
			s.ws.Close()
			return nil, err
		}
		for _, a := range s.recoveries {
			if a.Revision > s.contractRevision {
				s.contractRevision = a.Revision
			}
		}
		s.censorRecoveries("session_restarted_before_effect_observation")
	}
	s.eng = detect.NewEngine(cfg, s.engineContract(), s.acc.State == contract.StateAccepted, "live", root, id, "")
	s.eng.FirstPrompt = s.firstPrompt
	s.notifier = notify.New(cfg)
	s.hud = notify.NewHUDManager()
	if jc := cfg.Detectors.S3.Judge; jc.Provider != "" && (cfg.Privacy.ExternalJudge || isLocal(jc.BaseURL)) {
		if j, err := judge.New(judge.Config{Provider: jc.Provider, Model: jc.Model, BaseURL: jc.BaseURL, APIKeyEnv: jc.APIKeyEnv, LocalOnly: !cfg.Privacy.ExternalJudge}); err == nil {
			s.judge = j
		}
	}
	_ = s.ws.Watch(4000)
	go func() {
		ctx, cancel := withTimeout(60 * time.Second)
		defer cancel()
		_, _ = s.ws.Compute(ctx)
	}()
	go s.worker()
	return s, nil
}

func (s *Session) applyOverrides() {
	if s.db == nil {
		return
	}
	if s.Cfg.Detectors.Overrides == nil {
		s.Cfg.Detectors.Overrides = map[string]int{}
	}
	for _, rule := range []string{"s1.identical_rerun", "s2.stuck_error", "s4.read_only_streak"} {
		s.Cfg.Detectors.Overrides[rule] = OverrideSteps(s.db, s.Root, rule, time.Now())
	}
}

// OverrideSteps converts false-positive feedback into threshold steps: one
// step per 3 records, at most 2 steps (the 2x cap is applied by the engine),
// minus one step per 14 days without a new false positive.
func OverrideSteps(db *ledger.DB, project, rule string, now time.Time) int {
	n := db.FalsePositives(project, rule, time.Time{})
	steps := n / 3
	if steps > 2 {
		steps = 2
	}
	last := db.LastFalsePositive(project, rule)
	if !last.IsZero() {
		steps -= int(now.Sub(last).Hours() / (24 * 14))
	}
	if steps < 0 {
		steps = 0
	}
	return steps
}

func (s *Session) loadContract() {
	c, raw, err := contract.Load(s.Root)
	if s.storageErr != nil && s.acc.RequestedGoal != "" {
		// A failed revocation write must not reload the old on-disk approval.
		s.setContract(c, s.acc)
		return
	}
	if err != nil {
		s.setContract(nil, contract.Acceptance{State: contract.StateDraft})
		return
	}
	s.setContract(c, contract.CurrentState(s.Root, c, raw))
}

// setContract updates all contract consumers under s.mu (or before startup).
func (s *Session) setContract(c *contract.Contract, acc contract.Acceptance) {
	old, _ := json.Marshal(s.c)
	next, _ := json.Marshal(c)
	if string(old) != string(next) || s.acc.State != acc.State || !s.acc.AcceptedAt.Equal(acc.AcceptedAt) {
		s.censorRecoveries("intent_revision_changed")
		s.contractRevision++
		s.checks = map[string]CheckResult{}
		s.produceSinceCheck = 0
		s.pendingDraft = ""
		s.drifts = 0
	}
	s.c, s.acc = c, acc
	accepted := c != nil && acc.State == contract.StateAccepted
	if s.runner != nil {
		authority := ""
		if accepted {
			authority = contract.AuthorityDigest(c)
		}
		s.runner.SetAuthority(authority)
	}
	if s.eng != nil {
		s.eng.Contract, s.eng.Accepted = s.engineContract(), accepted
		s.eng.ScopeChanged()
	}
	if s.ws != nil {
		var scope []string
		if accepted {
			scope = c.Scope.Allow
		}
		s.ws.SetScope(scope)
	}
}

// engineContract returns the contract the detectors may use: only an
// accepted contract defines scope and budget.
func (s *Session) engineContract() *contract.Contract {
	if s.acc.State == contract.StateAccepted {
		return s.c
	}
	return nil
}

func (s *Session) shellBusy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.shell) > 0
}

// worker processes events in order, refreshing the workspace fingerprint
// before each one so detector input is exact.
func (s *Session) worker() {
	defer close(s.workerDone)
	var savedGap uint64
	for j := range s.queue {
		ev := j.ev
		s.mu.Lock()
		tool := ev.Kind == event.KindTool
		s.mu.Unlock()
		var after string
		if tool {
			if cur, fresh := s.ws.Current(); fresh {
				after = cur
			} else {
				ctx, cancel := withTimeout(60 * time.Second)
				if h, err := s.ws.Compute(ctx); err == nil {
					after = h
				}
				cancel()
			}
		}
		s.mu.Lock()
		if n := s.queueRejected.Load(); n > savedGap && s.db != nil {
			err := s.db.SaveObservationGap(s.Agent, s.ID, n)
			s.recordStorageError(err)
			if err == nil {
				savedGap = n
			}
		}
		if tool {
			if after != "" {
				ev.WSAfter = after
			}
			if ev.WSBefore == "" {
				// the call did not change files (verification, exploration)
				if !(ev.Category == event.CatProduce || ev.Mutating || ev.Unknown) {
					ev.WSBefore = ev.WSAfter
				}
			}
		}
		sigs := s.observeSafe(ev)
		s.mu.Unlock()
		j.done <- sigs
	}
}

// observeSafe is observe that survives a panic in one session: the event
// is counted as unprocessed, which marks the observation incomplete (no
// intervention, "확인 불가"), and the daemon keeps serving other sessions.
// Called with s.mu held.
func (s *Session) observeSafe(ev *event.Event) (sigs []detect.Signal) {
	defer func() {
		if r := recover(); r != nil {
			s.queueRejected.Add(1)
			fmt.Fprintf(os.Stderr, "samcheonpo: 세션 %s 사건 처리 중 오류: %v\n", s.ID, r)
			sigs = nil
		}
	}()
	return s.observe(ev)
}

func (s *Session) observe(ev *event.Event) []detect.Signal {
	// A previous pass is not current progress after an observed mutation.
	// The runner retains keyed evidence and may revalidate/reuse it at the next
	// checkpoint when the declared inputs really are unchanged.
	if ev.Kind == event.KindTool && (ev.Mutating || ev.Unknown || ev.Category == event.CatProduce || (ev.WSBefore != "" && ev.WSAfter != "" && ev.WSBefore != ev.WSAfter)) {
		s.checks = map[string]CheckResult{}
	}
	if ev.Usage.Total() > 0 {
		ev.CostMicroKRW, ev.Priced = s.prices.MicroKRW(ev.Usage, ev.TS)
	} else {
		ev.Priced = true
	}
	lastProgress := s.eng.St.LastProgress
	sigs := s.eng.Observe(ev)
	if s.eng.St.LastProgress != lastProgress && s.eng.St.LastProgress == ev.Seq && detect.ExecutedVerify(ev) && ev.ExitCode != nil && *ev.ExitCode == 0 {
		s.snapshotProgress(ev.Seq, ev.CmdNorm)
	}
	if s.usage == nil && ev.Kind == event.KindTool && ev.Tool != "checkpoint" {
		// agents without a Claude transcript report usage after the calls
		s.sinceTokens = append(s.sinceTokens, ev)
	}
	s.persistEvent(ev)
	s.trackOutcomes(ev)
	s.trackAttempts(ev)
	s.deliver(sigs)
	if !s.closed && s.queueRejected.Load() == 0 && ev.Kind == event.KindTool && s.judge != nil {
		s.toolsSinceJudge++
		if s.toolsSinceJudge >= max(1, s.Cfg.Detectors.S3.JudgeEveryEvents) && !s.judging {
			s.toolsSinceJudge = 0
			s.judging = true
			go s.runJudge()
		}
	}
	if !s.closed && s.Cfg.Rollout.Mode != "shadow" && ev.Category == event.CatProduce && s.Cfg.IronLaws.Enabled && ev.Kind == event.KindTool {
		go s.ironLawsCheck(ev)
	}
	if ev.Category == event.CatProduce {
		s.produceSinceCheck++
		if !s.closed && s.Cfg.Rollout.Mode != "shadow" && s.produceSinceCheck >= 5 && s.acc.State == contract.StateAccepted {
			s.produceSinceCheck = 0
			go s.checkpoint(true)
		}
	}
	return sigs
}

// deliver turns combined signals into pending agent nudges, user messages,
// notifications and ledger rows.
func (s *Session) deliver(sigs []detect.Signal) {
	for i := range sigs {
		v := sigs[i]
		if strings.HasPrefix(v.Rule, "s1.") {
			// Clone before enrichment: engine-owned facts must stay immutable.
			facts := make(map[string]any, len(v.Facts)+2)
			for key, value := range v.Facts {
				facts[key] = value
			}
			plan := s.verificationCandidates()
			facts["verification_plan"] = plan
			facts["verification_hint"] = verificationHint(plan)
			v.Facts = facts
		}
		if v.Rule == "s8.forced_no_progress" {
			// tell the user how to stop the auto-continue tool that is running
			for _, f := range rules.Current().AutoContinueFiles {
				if _, err := os.Stat(filepath.Join(s.Root, f.Path)); err == nil {
					v.Facts["stop_hint"] = f.Stop
				}
			}
		}
		s.persistVerdict(v)
		if !v.Primary || v.Level == detect.L0 || v.Suppressed {
			continue
		}
		cp := v
		s.lastPrimary = &cp
		if (s.Cfg.Rollout.Mode == "shadow" || slices.Contains(s.Cfg.Rollout.ShadowRules, v.Rule)) && !policy.ExplicitGuardrail(v.Rule) {
			s.record(v, "shadow")
			continue
		}
		ctx := s.msgContext()
		if v.Level >= detect.L2 {
			s.unresolved = &cp
		}
		if v.Level == detect.L1 && v.Arm == "none" {
			s.record(v, "none")
			continue
		}
		attempt, prescription, proposed := s.proposeRecovery(v)
		if !proposed {
			continue
		}
		if v.Arm != "fact" {
			if hint, ok := v.Facts["verification_hint"].(string); ok && hint != "" {
				prescription += "\n" + hint
			}
		}
		channel := "agent"
		if attempt.Route == "user" {
			channel = "user"
		}
		switch {
		case v.Level == detect.L1:
			m := intervene.Agent(v, ctx, "fact")
			if v.Arm != "fact" {
				m = strings.TrimSpace(m + "\n" + prescription)
			} else {
				m = recoveryMarker(attempt.ID) + "\n" + m
			}
			if attempt.Route == "user" {
				s.userMsg = append(s.userMsg, m)
			} else {
				s.pending = append(s.pending, m)
				s.pendingRule[m] = s.adviceKey(v)
			}
			s.record(v, channel)
		case v.Level >= detect.L2:
			if attempt.Route == "advice" {
				m := strings.TrimSpace(intervene.Agent(v, ctx, "fact") + "\n" + prescription)
				s.pending = append(s.pending, m)
				s.pendingRule[m] = s.adviceKey(v)
			}
			um := intervene.User(v, ctx)
			if attempt.Route == "user" {
				um += "\n" + prescription
			}
			s.userMsg = append(s.userMsg, um)
			s.notifier.Send("삼천포", firstLine(um))
			s.record(v, channel)
		}
	}
	s.broadcastHUD()
}

// adviceKey is called with s.mu held. It scopes advice to the current intent
// revision, the rule, its detected pattern and its target.
func (s *Session) adviceKey(v detect.Signal) string {
	var revision uint64
	if s.intentRevision != nil {
		revision = s.intentRevision.Number
	}
	kind, _ := v.Facts["kind"].(string)
	target, _ := v.Facts["path"].(string)
	if target == "" {
		target, _ = v.Facts["cmd"].(string)
	}
	return policy.EscalationKey(revision, v.Rule, kind, target)
}

// routeFor is called with s.mu held. It applies the launch stage, then
// escalates advice the agent already received for the same key when the rule
// may block in the current contract state.
func (s *Session) routeFor(v detect.Signal) (route string, escalated bool) {
	base := policy.RolloutRoute(s.Cfg.Rollout.Mode, v.Rule, s.Cfg.Rollout.ValidatedRules)
	if slices.Contains(s.Cfg.Rollout.ShadowRules, v.Rule) && !policy.ExplicitGuardrail(v.Rule) {
		return "observe", false
	}
	kind, _ := v.Facts["kind"].(string)
	advised := s.advised[s.adviceKey(v)] && policy.Escalable(v.Rule, kind, s.acc.State == contract.StateAccepted)
	route = policy.Escalate(base, advised, s.released[s.adviceKey(v)], s.escalations, s.Cfg.Rollout.EscalateMaxBlocks)
	return route, route == "block" && base != "block"
}

// escalationNote explains an escalated block to the agent and the user.
const escalationNote = "[삼천포] 같은 문제에 대한 권고가 이미 전달됐는데 반복되어 이번에는 막았다. 잘못된 판정이면 사용자가 /samcheonpo:keep normal로 해제할 수 있다."

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (s *Session) msgContext() intervene.Context {
	st := s.eng.St
	c := intervene.Context{IdleMicro: st.TotalMicro - st.ProgressMark, TotalMicro: st.TotalMicro}
	tokens, unpriced, _, idleTokens := s.measuredUsage()
	c.UsageUnknown = tokens == 0
	c.UnitTokens = unpriced > 0
	c.TotalTokens, c.IdleTokens = tokens, idleTokens
	if s.c != nil {
		c.Goal = s.c.Goal
		c.Scope = strings.Join(s.c.Scope.Allow, ", ")
	} else if s.firstPrompt != "" {
		c.Goal = "첫 요청(" + trunc(s.firstPrompt, 60) + ")"
	}
	c.Handoff = filepath.Join(contract.Dir(s.Root), "handoff")
	return c
}

func trunc(s string, n int) string {
	r := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

func (s *Session) record(v detect.Signal, channel string) {
	if s.db != nil {
		s.recordStorageError(s.db.RecordIntervention(v, s.ID, channel))
	}
}

func (s *Session) persistEvent(ev *event.Event) {
	if s.db == nil {
		return
	}
	if err := s.db.UpsertLive(s.ID, s.Agent, s.Root, s.Transcript, s.parser.Session.Model); err != nil {
		s.recordStorageError(err)
		return
	}
	s.recordStorageError(s.db.InsertLiveEvent(s.ID, ev))
}

func (s *Session) persistVerdict(v detect.Signal) {
	if s.db == nil {
		return
	}
	ctx := s.msgContext()
	s.recordStorageError(s.db.InsertLiveVerdict(s.ID, v, intervene.User(v, ctx), intervene.Agent(v, ctx, "prescription")))
}

// recordStorageError is called with s.mu held; retain the first lost write.
func (s *Session) recordStorageError(err error) {
	if err != nil && s.storageErr == nil {
		s.storageErr = fmt.Errorf("기록 저장 실패: %w", err)
		s.userMsg = append(s.userMsg, "삼천포 기록 저장에 실패했습니다. 진척과 영수증이 불완전할 수 있습니다.")
	}
}

// enqueue sends an event to the worker and waits up to wait for its signals.
func (s *Session) enqueue(ev *event.Event, wait time.Duration) {
	j := &job{ev: ev, done: make(chan []detect.Signal, 1)}
	s.queueMu.Lock()
	if s.queueClosed {
		s.queueMu.Unlock()
		return
	}
	select {
	case s.queue <- j:
	default:
		// Never hold a hook (or finalization) behind a saturated worker.
		// An incomplete observation stream cannot produce a valid receipt.
		s.queueRejected.Add(1)
		s.queueMu.Unlock()
		return
	}
	s.queueMu.Unlock()
	if wait <= 0 {
		return
	}
	select {
	case <-j.done:
	case <-time.After(wait):
	}
}

// takePending returns and clears queued agent nudges and user messages.
func (s *Session) takePendingFor(hook string, ack *ackSlot) (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	canInject := hook == "PreToolUse" && s.caps.InjectPre || hook == "PostToolUse" && s.caps.InjectPost || hook == "UserPromptSubmit" && s.caps.PromptInject || hook == "StopBlock" && s.caps.BlockStop
	var a, u string
	if canInject {
		if anchor := s.takeAnchor(true); anchor != "" {
			s.pending = append([]string{anchor}, s.pending...)
		}
		for _, m := range s.pending {
			if key := s.pendingRule[m]; key != "" {
				s.noteAdvice(ack, key)
			}
		}
		clear(s.pendingRule)
		a = strings.Join(s.pending, "\n\n")
		if s.pendingDraft != "" {
			a = strings.TrimSpace(s.pendingDraft + "\n\n" + a)
			s.pendingDraft = ""
		}
		s.pending = nil
	}
	if s.caps.UserMessage {
		u = strings.Join(s.userMsg, "\n\n")
		s.userMsg = nil
	}
	return a, u
}

// pollUsage attributes newly written transcript usage to events.
func (s *Session) pollUsage() {
	s.pollUsageMode(false)
}

func (s *Session) pollUsageMode(final bool) {
	if s.cx != nil {
		s.pollCodexMode(final)
		return
	}
	if s.usage == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed && !final {
		return
	}
	ups, err := s.usage.Poll()
	if err != nil || len(ups) == 0 {
		return
	}
	for _, u := range ups {
		ev := s.byTool[u.ToolUseID]
		if ev == nil && u.ToolUseID == "" {
			ev = s.msgEvents[u.MessageID]
		}
		if ev == nil {
			// usage of a message without tool calls, or of a call not seen by hooks
			ev = &event.Event{Kind: event.KindMessage, TS: time.Now(), Summary: "message", Basis: "live"}
			s.parser.AddEvent(ev)
			ev.Usage = u.Delta
			ev.CostMicroKRW, ev.Priced = s.prices.MicroKRW(u.Delta, ev.TS)
			if u.ToolUseID == "" {
				s.msgEvents[u.MessageID] = ev
			}
			sigs := s.eng.Observe(ev)
			s.persistEvent(ev)
			s.deliver(sigs)
			continue
		}
		before := ev.CostMicroKRW
		ev.Usage.Add(u.Delta)
		ev.CostMicroKRW, ev.Priced = s.prices.MicroKRW(ev.Usage, ev.TS)
		s.eng.AddCost(ev, ev.CostMicroKRW-before)
		if s.db != nil {
			s.recordStorageError(s.db.UpdateLiveCost(s.ID, ev))
		}
	}
}

// checkpoint runs contract checks and updates progress.
func (s *Session) checkpoint(mid bool) []CheckResult {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	s.mu.Lock()
	if s.closed || s.queueRejected.Load() > 0 {
		s.mu.Unlock()
		return nil
	}
	s.loadContract()
	c := s.c
	revision := s.contractRevision
	taskID, intentRevision := s.evidenceIdentity()
	accepted := s.acc.State == contract.StateAccepted
	side := map[string]bool{}
	for _, id := range s.acc.SideEffect {
		side[id] = true
	}
	s.mu.Unlock()
	if c == nil || !accepted {
		return nil
	}
	res := s.runner.RunTaskRevision(c.MachineChecks(), mid, side, taskID, intentRevision, s.ID)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadContract()
	currentTask, currentRevision := s.evidenceIdentity()
	if s.closed || s.queueRejected.Load() > 0 || revision != s.contractRevision || currentTask != taskID || currentRevision != intentRevision {
		for i := range res {
			res[i].Pass = false
			res[i].Skipped = "검사 중 계약이나 관측 상태가 바뀌어 결과를 적용하지 않았다"
		}
		return res
	}
	met, total, reused := 0, 0, 0
	newly := false
	for _, r := range res {
		if r.Evidence != nil && s.task != nil && s.db != nil {
			s.recordStorageError(s.db.SaveEvidence(*r.Evidence))
		}
		if r.Reused {
			reused++
		}
		if r.Skipped != "" {
			delete(s.checks, r.ID)
			total++
			continue
		}
		total++
		if r.SideEffect {
			s.acc.SideEffect = append(s.acc.SideEffect, r.ID)
			s.recordStorageError(contract.SaveAcceptance(s.Root, s.acc))
		}
		prev, had := s.checks[r.ID]
		s.checks[r.ID] = r
		if r.Pass && !r.SideEffect {
			met++
			if !had || !prev.Pass {
				newly = true
				// shown to the user only; the agent hears about reusable
				// evidence right before it would run the same check again
				if r.Evidence != nil {
					s.userMsg = append(s.userMsg, fmt.Sprintf("삼천포: 완료 조건 `%s`이(가) 통과했습니다(근거 %s). 관련 코드와 환경이 바뀌지 않으면 같은 검사를 다시 돌릴 필요가 없습니다.", r.Command, r.Evidence.ID))
				} else {
					s.userMsg = append(s.userMsg, fmt.Sprintf("삼천포: 완료 조건 `%s`이(가) 통과했습니다.", r.Command))
				}
			}
		}
	}
	ev := &event.Event{Kind: event.KindTool, Tool: "checkpoint", RawTool: "samcheonpo.checkpoint", TS: time.Now(), Category: event.CatWatch, Basis: "live",
		Summary: fmt.Sprintf("checkpoint %d/%d (재사용 %d)", met, total, reused), Priced: true}
	s.parser.AddEvent(ev)
	s.eng.ObserveWatch(ev)
	s.persistEvent(ev)
	if newly {
		s.eng.CheckpointProgress(ev)
		s.snapshotProgress(ev.Seq, ev.Summary)
	}
	if s.db != nil {
		s.recordStorageError(s.db.InsertProgress(s.ID, ev.Seq, met, total, res))
	}
	s.broadcastHUD()
	return res
}

// criteria returns met/total from the last checkpoint results.
func (s *Session) criteria() (int, int) {
	if s.c == nil || s.acc.State != contract.StateAccepted {
		return 0, 0
	}
	if s.queueRejected.Load() > 0 {
		return 0, len(s.c.Done)
	}
	met := 0
	checks := s.c.MachineChecks()
	for _, c := range checks {
		if r, ok := s.checks[c.ID]; ok && r.Pass && !r.SideEffect {
			met++
		}
	}
	// Manual criteria remain unconfirmed; machine checks never satisfy them.
	return met, len(s.c.Done)
}

// Statusline renders the status line.
func (s *Session) Statusline() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.hudState()
	return "[" + h.StatusText + "] " + h.Summary
}

// stateLabel condenses the session into one of four states a non-developer
// can act on. "순조로움" needs progress evidence: the absence of warnings is
// never shown as health, and incomplete observation is shown as unknown.
func (s *Session) stateLabel(met, total int) string {
	st := s.eng.St
	if s.queueRejected.Load() > 0 || s.storageErr != nil || s.preLate.Load() > 0 {
		return "확인 불가"
	}
	if s.unresolved != nil {
		return "지금 끼어드세요"
	}
	if p := s.lastPrimary; p != nil && p.Seq > st.LastProgress {
		if p.Level >= detect.L2 {
			return "지금 끼어드세요"
		}
		return "지켜보는 중"
	}
	if met > 0 || (total == 0 && len(st.ProgressSeqs) > 0) {
		return "순조로움"
	}
	return "지켜보는 중"
}

// measuredUsage is called under s.mu and never mistakes unobserved usage for
// a free session. One pass also avoids the old nested waste lookup.
func (s *Session) measuredUsage() (tokens, unpriced, waste, idleTokens int64) {
	return s.eng.St.UsageSummary()
}

// Finalize performs hindsight reclassification, saves the ledger and writes
// the receipt file (SessionEnd).
func (s *Session) Finalize() (string, error) {
	s.finalizeMu.Lock()
	defer s.finalizeMu.Unlock()
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	if s.finalized {
		p := s.receiptPath
		s.mu.Unlock()
		return p, nil
	}
	s.closed = true
	judgeWasPending := s.judgeBudget.Pending
	if judgeWasPending {
		if s.judgeCancel != nil {
			s.judgeCancel()
		}
		s.judgeBudget.Settle(0, false)
		s.judgeBudgetReason = "판정 중 세션 종료: 비용 미확인, 추가 호출 중단"
		s.recordStorageError(s.saveJudgeBudget())
	}
	s.mu.Unlock()
	s.queueMu.Lock()
	if !s.queueClosed {
		s.queueClosed = true
		close(s.queue)
	}
	s.queueMu.Unlock()
	<-s.workerDone
	// Attribute the final transcript delta only after queued calls are known.
	s.pollUsageMode(true)
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	s.ws.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.censorRecoveries("session_ended_before_effect_observation")
	if judgeWasPending {
		s.recordUnfinishedJudge()
	}
	if s.storageErr != nil {
		return "", s.storageErr
	}
	if n := s.queueRejected.Load(); n > 0 {
		if s.db != nil {
			if err := s.db.SaveObservationGap(s.Agent, s.ID, n); err != nil {
				return "", fmt.Errorf("관측 누락 기록 저장 실패: %w", err)
			}
		}
		return "", fmt.Errorf("관측 큐 포화로 %d건을 처리하지 못해 완전한 영수증을 발행할 수 없습니다", n)
	}
	sess := s.parser.Session
	sess.Mode = "live"
	if sess.StartedAt.IsZero() && len(st0(s)) > 0 {
		sess.StartedAt = st0(s)[0].TS
	}
	sess.EndedAt = time.Now()
	sess.FirstPrompt = s.firstPrompt
	res := analyze.Finalize(sess, s.eng, s.CfgHash, s.prices.Version)
	if met, total := s.criteria(); total > 0 {
		res.Criteria.Met, res.Criteria.Total = met, total
		if met > 0 {
			res.Grade = analyze.GradeVerified
		}
	}
	if s.c != nil && s.acc.State == contract.StateAccepted {
		res.ContractHash = s.c.ChecksHash()
	}
	if s.db == nil {
		s.finalized = true
		return "", nil
	}
	if err := analyze.CheckInvariant(sess, res.Totals); err != nil {
		return "", err
	}
	sl, err := s.db.SaveAnalysis(res, 0, 0, nil)
	if err != nil {
		return "", err
	}
	billing := cost.ObserveSession(sess, s.prices.Version)
	if s.judgeBudget.Unknown {
		billing.UsageComplete = false
		billing.Missing = append(billing.Missing, "감시 판정 비용 미확인: 예약을 실제 지출이나 무료로 확정하지 않는다")
	}
	rc := receipt.Build(receipt.Input{Title: receipt.SessionTitle(sess.StartedAt, s.ID, s.Agent), Agent: s.Agent, Totals: res.Totals, Grade: res.Grade,
		Verdicts: res.Verdicts, Seal: &sl, Criteria: [2]int{res.Criteria.Met, res.Criteria.Total}, Billing: billing, Recoveries: s.recoveries})
	dir := filepath.Join(config.Home(), "receipts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, s.ID+".md")
	if err := os.WriteFile(p, []byte(rc.Markdown()), 0o600); err != nil {
		return "", err
	}
	s.finalized, s.receiptPath = true, p
	return p, nil
}

func st0(s *Session) []*event.Event { return s.eng.St.Events }

// handoffDoc writes an L4 handoff document.
func (s *Session) handoffDoc(reason string) string {
	dir := filepath.Join(contract.Dir(s.Root), "handoff")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	st := s.eng.St
	var b strings.Builder
	fmt.Fprintf(&b, "# 인수인계 %s\n\n", time.Now().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "사유: %s\n\n", reason)
	if s.c != nil {
		fmt.Fprintf(&b, "## 계약\n\n- 목표: %s\n", s.c.Goal)
		for _, d := range s.c.Done {
			state := "미확인"
			if r, ok := s.checks[d.ID]; ok {
				if r.Pass {
					state = "충족"
				} else {
					state = "실패"
				}
			}
			if d.Check != "" {
				fmt.Fprintf(&b, "- 완료 조건 `%s`: %s\n", d.Check, state)
			} else {
				fmt.Fprintf(&b, "- 완료 조건 (사람 확인) %s\n", d.Manual)
			}
		}
	} else if s.firstPrompt != "" {
		fmt.Fprintf(&b, "## 요청\n\n%s\n", trunc(s.firstPrompt, 400))
	}
	b.WriteString("\n## 진척 원장 요약\n\n")
	files := map[string]bool{}
	for _, ev := range st.Events {
		if ev.Category == event.CatProduce {
			for _, p := range ev.Paths {
				files[p] = true
			}
		}
	}
	var fl []string
	for f := range files {
		fl = append(fl, f)
	}
	sort.Strings(fl)
	fmt.Fprintf(&b, "- 바꾼 파일: %s\n", strings.Join(fl, ", "))
	fmt.Fprintf(&b, "- 지출: %s원, 마지막 진척 이후 %s원\n", contract.Comma(cost.Won(st.TotalMicro)), contract.Comma(cost.Won(st.TotalMicro-st.ProgressMark)))
	b.WriteString("\n## 막힌 문제\n\n")
	for _, v := range s.eng.Verdicts {
		if v.Primary && v.Level >= detect.L1 {
			fmt.Fprintf(&b, "- %s\n", receipt.Describe(v))
		}
	}
	if st.LastVerifyFailing() {
		b.WriteString("- 마지막 검증이 실패한 상태다\n")
	}
	b.WriteString("\n## 다음 세션 첫 지시문\n\n")
	goal := s.firstPrompt
	if s.c != nil {
		goal = s.c.Goal
	}
	fmt.Fprintf(&b, "이전 세션의 인수인계 문서(이 파일)를 읽고 이어서 진행한다. 목표: %s. 위 '막힌 문제'에 적힌 접근은 이미 시도되었다.\n", trunc(goal, 300))
	p := filepath.Join(dir, time.Now().Format("20060102-150405")+".md")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		return ""
	}
	return p
}

// classifyEvent applies first-pass classification with the session options.
func (s *Session) classifyEvent(ev *event.Event) {
	classify.Initial(ev, classify.Options{VerifyCommands: s.Cfg.VerifyCommands})
}

// pollCodex attributes rollout token deltas to the events observed since the
// previous record, like the retrospective Codex parser.
func (s *Session) pollCodex() {
	s.pollCodexMode(false)
}

func (s *Session) pollCodexMode(final bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed && !final {
		return
	}
	deltas, err := s.cx.Poll()
	if err != nil {
		return
	}
	sess := s.parser.Session
	sess.QuotaKnown = s.cx.Quota != nil
	sess.QuotaUsedPct, sess.QuotaWindowMin = 0, 0
	if q := s.cx.Quota; q != nil {
		sess.QuotaUsedPct, sess.QuotaWindowMin = q.UsedPct, q.WindowMinutes
	}
	for _, d := range deltas {
		targets := s.sinceTokens
		s.sinceTokens = nil
		if len(targets) == 0 {
			ev := &event.Event{Kind: event.KindMessage, TS: time.Now(), Summary: "message", Basis: "live", Usage: d}
			s.parser.AddEvent(ev)
			ev.CostMicroKRW, ev.Priced = s.prices.MicroKRW(d, ev.TS)
			sigs := s.eng.Observe(ev)
			s.persistEvent(ev)
			s.deliver(sigs)
			continue
		}
		n := int64(len(targets))
		for i, ev := range targets {
			x := event.Usage{In: d.In / n, Out: d.Out / n, CacheRead: d.CacheRead / n, CacheWrite: d.CacheWrite / n, Model: d.Model}
			if i == 0 {
				x.In += d.In % n
				x.Out += d.Out % n
				x.CacheRead += d.CacheRead % n
				x.CacheWrite += d.CacheWrite % n
			}
			before := ev.CostMicroKRW
			ev.Usage.Add(x)
			ev.CostMicroKRW, ev.Priced = s.prices.MicroKRW(ev.Usage, ev.TS)
			s.eng.AddCost(ev, ev.CostMicroKRW-before)
			if s.db != nil {
				s.recordStorageError(s.db.UpdateLiveCost(s.ID, ev))
			}
		}
	}
}

// codexExit applies the exit code recorded in the rollout (Codex hooks do not
// carry it). Without a record the output decides: error or failed-test lines
// count as a failure.
func (s *Session) codexExit(ev *event.Event) {
	if ev.Tool != event.ToolShell {
		return
	}
	code := -2
	if s.cx != nil {
		if c, ok := s.cx.Exits[ev.CallID]; ok {
			code = c
		}
	}
	if code == -2 {
		code = 0
		if len(ev.ErrFPs) > 0 || len(ev.FailedTests) > 0 {
			code = 1
		}
	}
	ev.ExitCode = &code
	ev.IsError = code != 0
	ev.ResultFP = fp.ResultFP(ev.ExitCode, ev.ErrFPs, ev.FailedTests)
}

// isLocal reports whether a judge endpoint is on this machine (no external
// transmission, so privacy.external_judge is not required).
func isLocal(base string) bool {
	return judge.IsLocalEndpoint(base)
}

// runJudge asks the semantic judge about the recent activity (S3).
// The judgement's own cost is a watch event and stops once it would exceed
// the configured share of the session's spend.
func (s *Session) runJudge() {
	defer func() {
		s.mu.Lock()
		s.judging = false
		s.mu.Unlock()
	}()
	s.mu.Lock()
	st := s.eng.St
	cfg := s.Cfg.Detectors.S3
	if s.closed || s.queueRejected.Load() > 0 || s.acc.State != contract.StateAccepted || s.c == nil || st.TotalMicro <= s.watchCost || s.judge == nil {
		s.mu.Unlock()
		return
	}
	if cfg.MaxWatchCostRatio <= 0 || cfg.MaxWatchCostRatio > 1 || math.IsNaN(cfg.MaxWatchCostRatio) || cfg.MaxWatchKRW <= 0 || cfg.MaxWatchKRW > 1000000 || cfg.JudgeTimeoutSec <= 0 || cfg.JudgeTimeoutSec > 60 || cfg.MaxJudgeInputBytes <= 0 || cfg.MaxJudgeInputBytes > 65536 {
		s.judgeBudgetReason = "판정 예산 설정이 유효하지 않아 호출하지 않았다"
		s.mu.Unlock()
		return
	}
	limit := min(int64(float64(st.TotalMicro-s.watchCost)*cfg.MaxWatchCostRatio), cfg.MaxWatchKRW*1000000)
	in := judge.Input{Goal: s.c.Goal}
	revision := s.contractRevision
	inputBytes := len(in.Goal)
	if inputBytes > cfg.MaxJudgeInputBytes {
		s.judgeBudgetReason = "판정 입력 크기 상한을 넘어 호출하지 않았다"
		s.mu.Unlock()
		return
	}
	for i := len(st.Events) - 1; i >= 0 && len(in.Recent) < 15; i-- {
		if ev := st.Events[i]; ev.Kind == event.KindTool && ev.Category != event.CatWatch {
			if len(ev.Summary) > cfg.MaxJudgeInputBytes-inputBytes {
				s.judgeBudgetReason = "판정 입력 크기 상한을 넘어 호출하지 않았다"
				s.mu.Unlock()
				return
			}
			inputBytes += len(ev.Summary)
			in.Recent = append([]string{ev.Summary}, in.Recent...)
		}
	}
	prompt := judge.Prompt(in)
	if len(prompt) > cfg.MaxJudgeInputBytes {
		s.judgeBudgetReason = "판정 입력 크기 상한을 넘어 호출하지 않았다"
		s.mu.Unlock()
		return
	}
	// Reserve a conservative byte-based input estimate plus framing allowance.
	// Provider billing can differ; an underestimate stops subsequent calls.
	model := judge.EffectiveModel(cfg.Judge.Provider, cfg.Judge.Model)
	estimate, priced := s.prices.MicroKRW(event.Usage{In: int64(len(prompt) + 4096), Out: 16, Model: model}, time.Now())
	if !priced {
		s.judgeBudgetReason = "판정 모델 단가가 미확인이라 호출하지 않았다"
		s.mu.Unlock()
		return
	}
	var intentRevision uint64
	if s.intentRevision != nil {
		intentRevision = s.intentRevision.Number
	}
	key := fp.Hash(fmt.Sprint(intentRevision), s.c.ChecksHash(), s.acc.FileHash, cfg.Judge.Provider, model, cfg.Judge.BaseURL, prompt)
	if err := s.judgeBudget.Reserve(key, estimate, limit, cfg.MaxJudgeCalls); err != nil {
		s.judgeBudgetReason = err.Error()
		s.mu.Unlock()
		return
	}
	if err := s.saveJudgeBudget(); err != nil {
		s.recordStorageError(err)
		s.judgeBudget.Settle(0, false)
		s.judgeBudgetReason = "판정 예산 예약 저장 실패: 호출하지 않았다"
		s.mu.Unlock()
		return
	}
	s.judgeBudgetReason = ""
	ctx, cancel := withTimeout(time.Duration(cfg.JudgeTimeoutSec) * time.Second)
	s.judgeCancel = cancel
	s.mu.Unlock()
	defer cancel()
	v, err := s.judge.Judge(ctx, in)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	} // finalization already preserved a pending charge as unknown
	actual, known := s.prices.MicroKRW(v.Usage, time.Now())
	validUsage := v.Usage.In >= 0 && v.Usage.Out >= 0 && v.Usage.CacheRead >= 0 && v.Usage.CacheWrite >= 0 && v.Usage.CacheWrite1h >= 0 && v.Usage.CacheWrite1h <= v.Usage.CacheWrite && v.Usage.Total() > 0 && actual >= 0
	known = known && err == nil && validUsage
	if !validUsage {
		v.Usage = event.Usage{}
		actual = 0
	}
	s.judgeBudget.Settle(actual, known)
	s.recordStorageError(s.saveJudgeBudget())
	if !known {
		s.judgeBudgetReason = "판정 실패 또는 사용량 누락: 비용 미확인, 추가 호출 중단"
	}
	if s.judgeBudget.Overrun {
		s.judgeBudgetReason = "계측된 판정 비용이 예약액을 넘어 추가 호출을 중단했다"
	}
	ev := &event.Event{Kind: event.KindTool, Tool: "judge", RawTool: "samcheonpo.judge", TS: time.Now(), Category: event.CatWatch, Basis: "live",
		Usage: v.Usage, Summary: "drift judge: " + v.Label}
	ev.CostMicroKRW, ev.Priced = s.prices.MicroKRW(v.Usage, ev.TS)
	if !known {
		ev.Summary = "drift judge: cost_unknown"
		ev.Priced = false
	}
	s.watchCost += ev.CostMicroKRW
	s.parser.AddEvent(ev)
	s.eng.ObserveWatch(ev)
	s.eng.AddCost(ev, ev.CostMicroKRW)
	s.persistEvent(ev)
	if !known {
		return
	}
	// Preserve incurred usage, but an old goal's verdict cannot judge a new
	// direction or restore scope enforcement while a draft awaits acceptance.
	if revision != s.contractRevision || s.acc.State != contract.StateAccepted {
		return
	}
	if v.Label == judge.Drift {
		s.drifts++
	} else {
		s.drifts = 0
	}
	if s.drifts == 2 {
		sig := detect.Signal{Detector: "S3", Rule: "s3.drift", Confidence: 0.7, Level: detect.L2, Evidence: []int64{ev.Seq},
			Facts: map[string]any{"count": 2}}
		s.deliver(s.eng.Emit(ev, []detect.Signal{sig}))
	}
}
