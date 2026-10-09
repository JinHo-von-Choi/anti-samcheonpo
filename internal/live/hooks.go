package live

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intervene"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/receipt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rules"
)

// HookInput is the common Claude Code hook payload.
type HookInput struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	Cwd            string          `json:"cwd"`
	HookEventName  string          `json:"hook_event_name"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	ToolUseID      string          `json:"tool_use_id"`
	Error          string          `json:"error"`
	Prompt         string          `json:"prompt"`
	StopHookActive bool            `json:"stop_hook_active"`
	LastMessage    string          `json:"last_assistant_message"`
	Source         string          `json:"source"`
	Trigger        string          `json:"trigger"`
	// ParentSessionID is lineage a plugin states when it spawned this session
	// for another one; it is evidence only when the plugin sends it.
	ParentSessionID string `json:"parent_session_id"`
	// Deadline is the hook client's wait limit, set by the daemon.
	Deadline time.Time `json:"-"`
	// Ack collects what this response tells the agent; it is recorded as
	// delivered only when the hook client confirms it printed the response.
	Ack *ackSlot `json:"-"`
	// Usage carries plugin-reported token usage (agents without a transcript).
	MessageID string `json:"message_id"`
	Model     string `json:"model"`
	Usage     *struct {
		In         int64 `json:"input_tokens"`
		Out        int64 `json:"output_tokens"`
		CacheRead  int64 `json:"cache_read_input_tokens"`
		CacheWrite int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// Wait budgets for the worker before the hook answers (the hook client has
// its own hard timeouts; anything not ready is delivered on the next hook).
const (
	postWait = 30 * time.Millisecond
)

func hookOut(m map[string]any) json.RawMessage {
	if len(m) == 0 {
		return nil
	}
	b, _ := json.Marshal(m)
	return b
}

func additional(event, text string) map[string]any {
	return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": event, "additionalContext": text}}
}

// handleHook dispatches one hook event and keeps only the decisions the
// agent's adapter declares it can carry (capability-based means selection).
func (d *Daemon) handleHook(agent, name string, in HookInput) (json.RawMessage, error) {
	out, s, err := d.dispatch(agent, name, in)
	if err != nil || s == nil || len(out) == 0 {
		return out, err
	}
	if s.queueRejected.Load() > 0 {
		return nil, nil // incomplete evidence must not authorize intervention
	}
	s.mu.Lock()
	caps := s.caps
	s.mu.Unlock()
	return FilterOutput(name, out, caps), nil
}

// FilterOutput drops hook output fields the agent cannot act on.
func FilterOutput(event string, raw json.RawMessage, c adapter.Caps) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	hs, _ := m["hookSpecificOutput"].(map[string]any)
	switch event {
	case "PreToolUse":
		if hs != nil && !c.BlockPre {
			delete(hs, "permissionDecision")
			delete(hs, "permissionDecisionReason")
		}
		if hs != nil && !c.InjectPre {
			delete(hs, "additionalContext")
		}
	case "PostToolUse", "PostToolUseFailure":
		if hs != nil && !c.InjectPost {
			delete(hs, "additionalContext")
		}
		if !c.EndTurnPost {
			delete(m, "continue")
			delete(m, "stopReason")
		}
		if !c.InjectPost {
			delete(m, "decision")
			delete(m, "reason")
		}
	case "UserPromptSubmit", "Inject":
		if hs != nil && !c.PromptInject {
			delete(hs, "additionalContext")
		}
	case "Stop":
		if !c.BlockStop {
			delete(m, "decision")
			delete(m, "reason")
		}
	}
	if hs != nil && len(hs) <= 1 {
		delete(m, "hookSpecificOutput")
	}
	if !c.UserMessage {
		delete(m, "systemMessage")
	}
	if len(m) == 0 {
		return nil
	}
	b, _ := json.Marshal(m)
	return b
}

func (d *Daemon) dispatch(agent, name string, in HookInput) (json.RawMessage, *Session, error) {
	if in.SessionID == "" {
		return nil, nil, nil
	}
	root := in.Cwd
	if root == "" {
		root = "."
	}
	if name == "SessionStart" {
		// An explicit resume may reuse an ID; late tool hooks may not.
		d.mu.Lock()
		delete(d.ended, in.SessionID)
		d.mu.Unlock()
	}
	s, err := d.session(in.SessionID, agent, root, in.TranscriptPath)
	if err != nil {
		return nil, nil, err
	}
	if name != "SessionEnd" {
		s.opMu.RLock()
		defer s.opMu.RUnlock()
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return nil, s, fmt.Errorf("이미 종료된 세션이다")
		}
	}
	s.mu.Lock()
	s.lastActive = time.Now()
	s.mu.Unlock()
	if name != "SessionEnd" {
		go s.pollUsage()
	}
	switch name {
	case "SessionStart":
		if in.Source == "compact" {
			s.mu.Lock()
			s.markCompaction()
			s.mu.Unlock()
		}
		if in.ParentSessionID != "" && in.ParentSessionID != in.SessionID {
			d.mu.Lock()
			limit := d.treeLimitLocked(s, in.ParentSessionID)
			d.mu.Unlock()
			d.swarm.register(in.SessionID, in.ParentSessionID, false, limit)
		}
		return nil, s, nil
	case "Usage":
		s.onUsage(in)
		return nil, s, nil
	case "UserPromptSubmit":
		return s.onPrompt(in), s, nil
	case "Inject":
		// a later model call within a turn (agy): hand over pending advice
		nudge, _ := s.takePendingFor("UserPromptSubmit", in.Ack)
		if nudge == "" {
			return nil, s, nil
		}
		return hookOut(additional("UserPromptSubmit", nudge)), s, nil
	case "PreToolUse":
		return s.onPreTool(in), s, nil
	case "PostToolUse", "PostToolUseFailure":
		return s.onPostTool(in, name == "PostToolUseFailure"), s, nil
	case "Stop":
		return s.onStop(in), s, nil
	case "PreCompact":
		ev := &event.Event{Kind: event.KindCompact, TS: time.Now(), Summary: "compact", Basis: "live"}
		s.mu.Lock()
		s.parser.AddEvent(ev)
		s.markCompaction()
		s.mu.Unlock()
		s.enqueue(ev, 0)
		return nil, s, nil
	case "SessionEnd":
		s.flushTurn()
		s.mu.Lock()
		endBudget := s.caps.SessionEndBudget
		s.mu.Unlock()
		if endBudget < 10*time.Second {
			// short hook budget (Codex): answer now, write the receipt after
			d.ending.Add(1)
			go func() {
				defer d.ending.Done()
				if _, err := s.Finalize(); err != nil {
					s.mu.Lock()
					s.recordStorageError(err)
					s.mu.Unlock()
				} else {
					d.drop(in.SessionID)
				}
			}()
			return nil, s, nil
		}
		p, err := s.Finalize()
		if err != nil {
			s.mu.Lock()
			s.recordStorageError(err)
			s.mu.Unlock()
			return nil, s, err
		}
		d.drop(in.SessionID)
		if p != "" {
			return hookOut(map[string]any{"systemMessage": "삼천포 영수증: " + p}), s, nil
		}
		return nil, s, nil
	}
	return nil, s, nil
}

func (s *Session) onPrompt(in HookInput) json.RawMessage {
	s.flushTurn()
	s.mu.Lock()
	ev := &event.Event{Kind: event.KindPrompt, TS: time.Now(), Summary: "prompt", Text: in.Prompt, Basis: "live"}
	ev.Forced = rules.Current().ForcedPrompts.Match(strings.TrimSpace(in.Prompt))
	s.parser.AddEvent(ev)
	s.prompts++
	newTask := s.prompts == 1 || s.acc.State == contract.StateDone
	if s.firstPrompt == "" && !strings.HasPrefix(strings.TrimSpace(in.Prompt), "/") {
		s.firstPrompt = in.Prompt
		s.eng.FirstPrompt = in.Prompt
	}
	s.stopBlocks = 0
	s.loadContract()
	needDraft := newTask && (s.c == nil || s.acc.State == contract.StateDone) && s.acc.State != contract.StateSkipped
	hasIntent := !needDraft || s.intentRevision != nil && s.acc.State != contract.StateDone
	change := intent.ClassifyChange(intent.User, in.Prompt, hasIntent)
	if change.Kind == intent.Redirect {
		acc, err := contract.RequestChange(s.Root, change.Goal)
		if err != nil {
			s.recordStorageError(err)
			// Fail closed in memory too; a storage error is not acceptance.
			acc = contract.Acceptance{State: contract.StateDraft, RequestedGoal: change.Goal}
		}
		s.setContract(s.c, acc)
		needDraft = true
	}
	if !ev.Forced {
		s.recordStorageError(s.recordIntent(change, intentSource(s, ev)))
	}
	card := s.goalCard(change.Goal)
	s.mu.Unlock()
	s.enqueue(ev, 0)
	nudge, user := s.takePendingFor("UserPromptSubmit", in.Ack)
	var ctx []string
	// Observation-only by default: ask for a draft only when the user turned
	// drafts on or already keeps a contract file.
	if needDraft && change.Kind != intent.Ignored && (s.Cfg.Contract.Draft == "on" || s.c != nil) {
		// The card and its command menu are for the user; the agent only gets
		// the draft request, so it never stops to wait for an acceptance.
		draft := contract.DraftRequest(contract.Candidates(s.Root), in.Prompt) + "\n" + draftContinueNote
		if card.RequestedGoal != "" {
			draft = "사용자 요청(초안에 반영할 내용): " + card.RequestedGoal + "\n" + draft
		}
		s.mu.Lock()
		if s.caps.PromptInject {
			ctx = append(ctx, draft)
		} else {
			s.pendingDraft = draft
		}
		s.mu.Unlock()
		user = strings.TrimSpace(card.Text() + "\n" + user)
	}
	if nudge != "" {
		ctx = append(ctx, nudge)
	}
	out := map[string]any{}
	if len(ctx) > 0 {
		out = additional("UserPromptSubmit", strings.Join(ctx, "\n\n"))
	}
	if user != "" {
		out["systemMessage"] = user
	}
	return hookOut(out)
}

// draftContinueNote keeps the agent on the requested work after drafting.
const draftContinueNote = "초안을 쓴 뒤 사용자의 수락이나 선택을 기다리지 말고 요청받은 작업을 바로 이어서 한다. 수락은 사용자가 따로 한다."

func (s *Session) buildTool(in HookInput) *event.Event {
	id := in.ToolUseID
	if id == "" {
		id = fmt.Sprintf("hook-%d", time.Now().UnixNano())
	}
	if in.Cwd != "" {
		s.parser.Cwd = in.Cwd
	}
	ev := s.parser.HookToolUse(in.ToolName, id, in.ToolInput, time.Now())
	s.classifyEvent(ev)
	return ev
}

// flushTurn closes, at a turn boundary, the calls that never received a
// post-tool hook (Codex apply_patch, calls the agent abandoned). Calls of one
// turn may run in parallel, so a new pre-tool hook is no evidence that an
// earlier call ended.
func (s *Session) flushTurn() {
	s.mu.Lock()
	stale := s.flushAwaiting("")
	s.mu.Unlock()
	for _, ev := range stale {
		s.enqueue(ev, 0)
	}
}

// flushAwaiting finalizes earlier calls that never received a post-tool hook:
// they are kept for cost but marked as not executed, so they count neither as
// writes nor as checks. A late result for a flushed call is ignored.
func (s *Session) flushAwaiting(except string) []*event.Event {
	var out []*event.Event
	for id, ev := range s.awaiting {
		if id == except {
			continue
		}
		delete(s.awaiting, id)
		s.flushed[id] = true
		delete(s.shell, id)
		x := -1
		ev.ExitCode = &x
		ev.Category = event.CatPlan
		ev.Summary += " (결과 없음)"
		out = append(out, ev)
	}
	return out
}

// preHookBudget mirrors the hook client's PreToolUse wait for a request that
// carries no deadline; preMargin leaves time to encode and write the reply.
const (
	preHookBudget = 15 * time.Millisecond
	preMargin     = 2 * time.Millisecond
)

func preDeadline(d time.Time) time.Time {
	if d.IsZero() {
		return time.Now().Add(preHookBudget)
	}
	return d
}

func (s *Session) onPreTool(in HookInput) json.RawMessage {
	deadline := preDeadline(in.Deadline)
	s.mu.Lock()
	s.loadContract()
	ev := s.buildTool(in)
	if ev.Tool == event.ToolShell && userOnlyCommand(ev.Cmd) {
		// refused whatever the rollout stage: this protects the user's own
		// decisions, not a waste estimate
		s.byTool[ev.CallID] = ev
		ev.ExitCode = intPtr(-1)
		blockPre := s.caps.BlockPre
		s.mu.Unlock()
		s.enqueue(ev, 0)
		if !blockPre {
			return hookOut(additional("PreToolUse", userOnlyReason))
		}
		return hookOut(map[string]any{"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": userOnlyReason,
		}})
	}
	if ev.Category == event.CatProduce {
		s.trackBefore(in, ev.CallID, ev.Paths)
	}
	s.awaiting[ev.CallID] = ev
	s.byTool[ev.CallID] = ev
	if cur, fresh := s.ws.Current(); fresh {
		ev.WSBefore = cur
	}
	if ev.Tool == event.ToolShell {
		s.shell[ev.CallID] = true
	}
	var deny bool
	var sig *detect.Signal
	if s.queueRejected.Load() == 0 && (ev.WSBefore != "" || ev.Category == event.CatProduce) {
		deny, sig = s.eng.PreCheck(ev)
	}
	if !deny && s.queueRejected.Load() == 0 {
		if pre := s.attemptPre(ev); pre != nil {
			deny, sig = true, pre
		}
	}
	if !deny {
		// the session ceiling, releases, long runs and whole-suite runs do
		// not depend on the workspace fingerprint
		if pre := s.eng.PreGuard(ev); pre != nil {
			deny, sig = true, pre
		}
	}
	if !deny {
		if pre := s.swarmPre(); pre != nil {
			deny, sig = true, pre
		}
	}
	ctx := s.msgContext()
	seq, tool, execFP, certain, norm := ev.Seq, ev.Tool, ev.ExecFP, ev.ExecCertain, ev.CmdNorm
	s.mu.Unlock()
	if !deny && s.queueRejected.Load() == 0 {
		if tool == event.ToolShell {
			if pre := s.evidencePre(execFP, certain, norm, seq, deadline); pre != nil {
				deny, sig = true, pre
			}
		}
	}
	if !deny {
		nudge, user := s.takePendingFor("PreToolUse", in.Ack)
		out := map[string]any{}
		if nudge != "" {
			out = additional("PreToolUse", nudge)
		}
		if user != "" {
			out["systemMessage"] = user
		}
		return hookOut(out)
	}
	v := *sig
	v.Seq = ev.Seq
	v.ID = fmt.Sprintf("%s-pre-%d", s.ID, ev.Seq)
	v.Primary = true
	if strings.HasPrefix(v.Rule, "s1.") {
		// the passing evidence is shown to the agent right before a rerun
		s.mu.Lock()
		hint := verificationHint(s.verificationCandidates())
		s.mu.Unlock()
		if hint != "" {
			facts := make(map[string]any, len(v.Facts)+1)
			for k, x := range v.Facts {
				facts[k] = x
			}
			facts["verification_hint"] = hint
			v.Facts = facts
		}
	}
	if v.Level == detect.L1 && s.Cfg.Experiment.Enabled {
		v.Arm = detect.Arm(s.ID, v.Rule)
	}
	if v.Arm == "none" {
		// control arm of the intervention experiment: record, do not block
		s.mu.Lock()
		s.persistVerdict(v)
		s.record(v, "none")
		s.mu.Unlock()
		return nil
	}
	reason := intervene.Agent(v, ctx, v.Arm)
	if reason == "" {
		reason = receipt.Describe(v)
	}
	s.mu.Lock()
	lp := v
	s.lastPrimary = &lp // the user can keep/steer the verdict they just saw
	route, escalated := s.routeFor(v)
	if route != "block" || !s.caps.BlockPre {
		if route == "block" {
			route = "advice" // the agent cannot be blocked here
		}
		s.persistVerdict(v)
		s.record(v, route)
		if route == "advice" && s.caps.InjectPre {
			s.noteAdvice(in.Ack, s.adviceKey(v))
		}
		s.broadcastHUD()
		s.mu.Unlock()
		if route == "observe" {
			return nil
		}
		return hookOut(additional("PreToolUse", reason))
	}
	if escalated {
		s.noteEscalation(in.Ack)
		reason = escalationNote + "\n" + reason
	}
	if time.Now().After(deadline) {
		s.preLate.Add(1)
	}
	delete(s.shell, ev.CallID)
	ev.ExitCode = intPtr(-1) // blocked, never executed
	s.persistVerdict(v)
	s.record(v, "agent")
	if v.Level >= detect.L3 {
		s.unresolved = &v
	}
	s.broadcastHUD()
	s.mu.Unlock()
	return hookOut(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       "deny",
		"permissionDecisionReason": reason,
	}})
}

func intPtr(i int) *int { return &i }

func (s *Session) onPostTool(in HookInput, failure bool) json.RawMessage {
	s.mu.Lock()
	if s.flushed[in.ToolUseID] {
		// already recorded once as a call without a result
		s.mu.Unlock()
		return nil
	}
	ev := s.byTool[in.ToolUseID]
	started := time.Time{}
	if ev == nil {
		ev = s.buildTool(in)
		s.byTool[ev.CallID] = ev
	} else {
		started = ev.TS // set when the pre-tool hook saw the call
	}
	delete(s.shell, ev.CallID)
	delete(s.awaiting, ev.CallID)
	resp := in.ToolResponse
	errText := in.Error
	if failure && errText == "" {
		errText = "Error"
	}
	s.parser.HookToolResult(ev.CallID, resp, failure, errText)
	if ev.ExitCode != nil && *ev.ExitCode == -1 && !failure {
		ev.ExitCode = nil
	}
	cx := s.cx
	s.mu.Unlock()
	if cx != nil {
		s.pollCodex() // the rollout holds this command's exit code
	}
	s.mu.Lock()
	if !s.caps.ExitInHook {
		s.codexExit(ev)
	}
	if ev.Category == event.CatProduce && !failure {
		s.trackAfter(ev.CallID, ev.Paths)
	}
	ev.TS = time.Now()
	if !started.IsZero() && ev.DurationMS == 0 {
		ev.DurationMS = ev.TS.Sub(started).Milliseconds()
	}
	contractWrite := false
	for _, p := range ev.Paths {
		if p == ".samcheonpo/contract.yml" || strings.HasSuffix(p, "/.samcheonpo/contract.yml") || filepath.Clean(p) == contract.Path(s.Root) {
			contractWrite = true
		}
	}
	s.mu.Unlock()
	if ev.Category == event.CatProduce || ev.Mutating || ev.Unknown {
		s.ws.Invalidate()
	}
	s.enqueue(ev, postWait)
	out := map[string]any{}
	if contractWrite {
		if o := s.onContractWritten(); o != nil {
			return o
		}
	}
	nudge, user := s.takePendingFor("PostToolUse", in.Ack)
	s.mu.Lock()
	stop := s.unresolved != nil && s.unresolved.Level >= detect.L3 && s.unresolved.Seq >= ev.Seq-1
	var l3 *detect.Signal
	escalatedStop := false
	if stop {
		l3 = s.unresolved
		route, escalated := s.routeFor(*l3)
		if route != "block" {
			l3 = nil
		} else if escalated {
			s.escalations++
			escalatedStop = true
		}
	}
	if l3 != nil && l3.Level == detect.L4 {
		if p := s.handoffDoc(receipt.Describe(*l3)); p != "" {
			user = strings.TrimSpace(user + "\n인수인계 문서: " + p)
		}
	}
	s.mu.Unlock()
	if l3 != nil {
		reason := user
		s.mu.Lock()
		if reason == "" {
			reason = intervene.User(*l3, s.msgContext())
		}
		if escalatedStop {
			reason = escalationNote + "\n" + reason
		}
		endTurnPost := s.caps.EndTurnPost
		s.mu.Unlock()
		if !endTurnPost {
			// no turn end from a post-tool hook (Codex): block with the reason
			// for the agent and show the user message
			return hookOut(map[string]any{"decision": "block", "reason": nudge + "\n" + reason, "systemMessage": reason})
		}
		return hookOut(map[string]any{"continue": false, "stopReason": reason})
	}
	if nudge != "" {
		out = additional("PostToolUse", nudge)
	}
	if user != "" {
		out["systemMessage"] = user
	}
	return hookOut(out)
}

// onContractWritten validates a freshly written contract and asks for
// confirmation when required.
func (s *Session) onContractWritten() json.RawMessage {
	b, err := readFile(contract.Path(s.Root))
	if err != nil {
		return nil
	}
	c, errs := contract.Parse(b)
	if len(errs) > 0 {
		s.mu.Lock()
		s.loadContract()
		s.mu.Unlock()
		var lines []string
		for _, e := range errs {
			lines = append(lines, e.Error())
		}
		return hookOut(additional("PostToolUse", "[삼천포] .samcheonpo/contract.yml 검증 결과: "+strings.Join(lines, "; ")+". 이 항목을 고치면 계약으로 쓸 수 있다."))
	}
	s.mu.Lock()
	acc := contract.Acceptance{State: contract.StateDraft}
	acc.RequestedGoal, acc.PreviousFileHash = s.acc.RequestedGoal, s.acc.PreviousFileHash
	s.recordStorageError(contract.SaveAcceptance(s.Root, acc))
	s.setContract(c, acc)
	need := s.Cfg.Contract.Confirm == "always" ||
		(s.Cfg.Contract.Confirm == "budget_over" && (c.Budget.KRW >= s.Cfg.Contract.ConfirmOverKRW || len(c.Scope.Protect) > 0))
	if s.db != nil {
		s.recordStorageError(s.db.RecordContract(s.ID, c.ChecksHash(), "draft"))
	}
	card := s.goalCard("")
	s.mu.Unlock()
	note := "[삼천포] 계약 초안을 받았다. 사용자가 /samcheonpo:accept로 수락하면 진척 판정 기준이 된다. 수락 전에는 검사를 자동 실행하지 않으니 원래 작업을 이어서 하면 된다."
	if !need {
		return hookOut(additional("PostToolUse", note))
	}
	if s.Cfg.Contract.Confirm == "always" {
		return hookOut(map[string]any{"continue": false, "stopReason": card.Text()})
	}
	// The draft stays unaccepted, so showing the card is enough; stopping the
	// turn would end unattended runs before the requested work is done.
	out := additional("PostToolUse", note)
	out["systemMessage"] = card.Text()
	return hookOut(out)
}

func (s *Session) onStop(in HookInput) json.RawMessage {
	s.flushTurn()
	s.pollUsage()
	s.mu.Lock()
	shadow := s.Cfg.Rollout.Mode == "shadow"
	stopRoute, stopEscalated := s.routeFor(detect.Signal{Rule: "s5.stop_unmet"})
	canBlock := stopRoute == "block" && s.caps.BlockStop
	s.mu.Unlock()
	var unmet []string
	if !shadow {
		results := s.checkpoint(false)
		for _, r := range results {
			if r.Skipped == "" && !r.Pass {
				unmet = append(unmet, r.Command)
			}
		}
	}
	s.mu.Lock()
	text := in.LastMessage
	if text == "" && s.usage != nil {
		text = s.usage.LastText
	}
	msg := &event.Event{Kind: event.KindMessage, TS: time.Now(), Summary: "message", Text: text, Basis: "live", Priced: true}
	s.parser.AddEvent(msg)
	s.mu.Unlock()
	s.enqueue(msg, time.Second)
	s.mu.Lock()
	sigs := s.eng.TurnEnd(msg, unmet)
	s.deliver(sigs)
	blocks := s.stopBlocks
	s.mu.Unlock()
	route := "Stop"
	if canBlock && len(unmet) > 0 && blocks < s.Cfg.Levels.MaxStopBlocks && !in.StopHookActive {
		route = "StopBlock"
	}
	nudge, user := s.takePendingFor(route, in.Ack)
	if canBlock && len(unmet) > 0 && blocks < s.Cfg.Levels.MaxStopBlocks && !in.StopHookActive {
		s.mu.Lock()
		s.stopBlocks++
		sig := detect.Signal{Detector: "S5", Rule: "s5.stop_unmet", Confidence: 1, Level: detect.L1, Evidence: []int64{msg.Seq},
			Facts: map[string]any{"unmet": unmet, "count": len(unmet), "blocks": s.stopBlocks}}
		sigs := s.eng.Emit(msg, []detect.Signal{sig})
		var reason string
		for _, v := range sigs {
			s.persistVerdict(v)
			s.record(v, "agent")
			reason = intervene.Agent(v, s.msgContext(), "prescription")
		}
		if stopEscalated {
			s.escalations++
			reason = escalationNote + "\n" + reason
		}
		s.mu.Unlock()
		if nudge != "" {
			reason = nudge + "\n\n" + reason
		}
		return hookOut(map[string]any{"decision": "block", "reason": reason})
	}
	out := map[string]any{}
	if len(unmet) > 0 && (!canBlock || blocks >= s.Cfg.Levels.MaxStopBlocks) {
		s.mu.Lock()
		met, total := s.criteria()
		s.mu.Unlock()
		user = strings.TrimSpace(user + fmt.Sprintf("\nAI가 끝났다고 했지만 완료 조건 %d개 중 %d개가 실패한 상태입니다.", total, total-met))
	}
	if user != "" {
		out["systemMessage"] = user
	}
	return hookOut(out)
}

// onUsage records cumulative token usage reported per message by a plugin.
// A message's usage belongs to the calls it issued (those seen since the
// previous message); a message without calls becomes a message event. Only
// the increase since the last report is added.
func (s *Session) onUsage(in HookInput) {
	if in.Usage == nil || in.MessageID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := event.Usage{In: in.Usage.In, Out: in.Usage.Out, CacheRead: in.Usage.CacheRead, CacheWrite: in.Usage.CacheWrite, Model: in.Model}
	targets, known := s.usageTargets[in.MessageID]
	if !known {
		targets = s.sinceTokens
		s.sinceTokens = nil
		if len(targets) == 0 {
			ev := &event.Event{Kind: event.KindMessage, TS: time.Now(), Summary: "message", Basis: "live", Priced: true}
			s.parser.AddEvent(ev)
			s.eng.Observe(ev)
			s.persistEvent(ev)
			targets = []*event.Event{ev}
		}
		s.usageTargets[in.MessageID] = targets
	}
	prev := s.usageSeen[in.MessageID]
	d := event.Usage{In: cur.In - prev.In, Out: cur.Out - prev.Out, CacheRead: cur.CacheRead - prev.CacheRead, CacheWrite: cur.CacheWrite - prev.CacheWrite, Model: cur.Model}
	s.usageSeen[in.MessageID] = cur
	if d.Total() == 0 {
		return
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
		if ev.Usage.Model == "" {
			ev.Usage.Model = d.Model
		}
		ev.CostMicroKRW, ev.Priced = s.prices.MicroKRW(ev.Usage, ev.TS)
		s.eng.AddCost(ev, ev.CostMicroKRW-before)
		if s.db != nil {
			s.recordStorageError(s.db.UpdateLiveCost(s.ID, ev))
		}
	}
}
