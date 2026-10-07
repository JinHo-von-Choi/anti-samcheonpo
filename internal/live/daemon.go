package live

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intervene"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/receipt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sockpath"
)

// ProtocolVersion is the socket protocol version.
const ProtocolVersion = 1

// Request is one line of the socket protocol.
type Request struct {
	V       int             `json:"v"`
	Agent   string          `json:"agent"`
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
	// DeadlineUnixMs is when the hook client stops waiting (0: older client).
	DeadlineUnixMs int64 `json:"deadline_unix_ms,omitempty"`
}

// Response is the reply line. Output is printed verbatim by the hook client.
type Response struct {
	DeliveryToken string          `json:"delivery_token,omitempty"`
	V             int             `json:"v"`
	Output        json.RawMessage `json:"output,omitempty"`
	Text          string          `json:"text,omitempty"`
	Error         string          `json:"error,omitempty"`
}

// SocketPath returns the daemon socket path.
func SocketPath() string { return sockpath.Path() }

// LockPath returns the daemon lock path.
func LockPath() string { return filepath.Join(config.Home(), "run", "daemon.lock") }

// Daemon holds live sessions.
type Daemon struct {
	mu           sync.Mutex
	sessions     map[string]*Session
	versions     map[string]string // agent -> probed version
	versionProbe func(string) string
	versionReady map[string]bool
	db           *ledger.DB
	prices       *cost.Table
	last         time.Time
	Idle         time.Duration
	ln           net.Listener
	ended        map[string]bool
	serving      sync.WaitGroup
	ending       sync.WaitGroup
	done         chan struct{}
}

// Run starts the daemon (blocking). It exits after Idle without requests.
func Run(dbPath string, idle time.Duration) error {
	if err := os.MkdirAll(filepath.Join(config.Home(), "run"), 0o700); err != nil {
		return err
	}
	lf, err := os.OpenFile(LockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("데몬이 이미 실행 중이다")
	}
	// the lock file names the holder so tools can stop this daemon
	_ = lf.Truncate(0)
	_, _ = lf.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	sock := SocketPath()
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	_ = os.Chmod(sock, 0o600)
	db, err := ledger.Open(dbPath)
	if err != nil {
		ln.Close()
		return err
	}
	defer db.Close()
	prices, err := cost.Load(filepath.Join(config.Home(), "prices.yml"))
	if err != nil {
		ln.Close()
		return err
	}
	d := &Daemon{sessions: map[string]*Session{}, versions: map[string]string{}, db: db, prices: prices, last: time.Now(), Idle: idle, ln: ln, done: make(chan struct{})}
	defer close(d.done)
	go d.idleWatch()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)
	go func() {
		select {
		case <-sigs:
			d.ln.Close()
		case <-d.done:
		}
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			continue
		}
		d.serving.Add(1)
		go func() { defer d.serving.Done(); d.serve(c) }()
	}
	d.serving.Wait()
	d.ending.Wait()
	d.mu.Lock()
	var sessions []*Session
	for _, s := range d.sessions {
		sessions = append(sessions, s)
	}
	d.mu.Unlock()
	var finalErr error
	for _, s := range sessions {
		if _, err := s.Finalize(); err != nil {
			finalErr = errors.Join(finalErr, fmt.Errorf("세션 %s: %w", s.ID, err))
		}
	}
	_ = os.Remove(sock)
	return finalErr
}

func (d *Daemon) idleWatch() {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-t.C:
		}
		d.mu.Lock()
		idle := time.Since(d.last) > d.Idle
		d.mu.Unlock()
		if idle {
			d.ln.Close()
			return
		}
	}
}

func (d *Daemon) serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Minute))
	rd := bufio.NewReaderSize(c, 1<<20)
	line, err := rd.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	d.mu.Lock()
	d.last = time.Now()
	d.mu.Unlock()
	var req Request
	var slot *ackSlot
	resp := Response{V: ProtocolVersion}
	if err := json.Unmarshal(line, &req); err != nil {
		resp.Error = "잘못된 요청: " + err.Error()
	} else if req.V != ProtocolVersion {
		resp.Error = fmt.Sprintf("지원하지 않는 프로토콜 버전 %d", req.V)
	} else {
		var err error
		resp.Output, resp.Text, slot, err = d.handleAck(req)
		if err != nil {
			resp.Error = err.Error()
		}
	}
	s := d.recoverySession(req, resp.Output)
	if s != nil || (slot.pending() && len(resp.Output) > 0) {
		var token [16]byte
		if _, err := rand.Read(token[:]); err == nil {
			resp.DeliveryToken = hex.EncodeToString(token[:])
		}
	}
	b, _ := json.Marshal(resp)
	wire := append(b, '\n')
	if n, err := c.Write(wire); err != nil || n != len(wire) {
		return
	}
	if resp.DeliveryToken == "" {
		return
	}
	if s != nil {
		s.markRecoveryOutput(resp.Output, recovery.Emitted)
	}
	// Responses that carry advice, a block or a recovery request a one-way
	// receipt. The client sends it after printing; a client that never got
	// the response (deadline passed, output failed) sends none, and a missing
	// receipt never becomes delivery proof. This wait runs after the reply
	// and does not delay the hook.
	_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	ackLine, err := rd.ReadBytes('\n')
	if err != nil {
		return
	}
	var ack struct {
		DeliveryToken string `json:"delivery_token"`
		Printed       bool   `json:"printed"`
	}
	if json.Unmarshal(ackLine, &ack) == nil && ack.Printed && ack.DeliveryToken == resp.DeliveryToken {
		if s != nil {
			s.markRecoveryOutput(resp.Output, recovery.Delivered)
		}
		slot.confirm()
	}
}

func (d *Daemon) handle(req Request) (json.RawMessage, string, error) {
	out, text, _, err := d.handleAck(req)
	return out, text, err
}

// handleAck is handle with the delivery slot of a hook response.
func (d *Daemon) handleAck(req Request) (json.RawMessage, string, *ackSlot, error) {
	slot := &ackSlot{}
	out, text, err := d.handleWith(req, slot)
	return out, text, slot, err
}

func (d *Daemon) handleWith(req Request, slot *ackSlot) (json.RawMessage, string, error) {
	switch req.Event {
	case "Ping":
		return nil, "pong", nil
	case "ObservationStatus":
		var in struct {
			Session string `json:"session_id"`
		}
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return nil, "", err
		}
		d.mu.Lock()
		s := d.sessions[in.Session]
		d.mu.Unlock()
		if s == nil {
			return nil, "", errors.New("세션을 찾을 수 없다")
		}
		s.mu.Lock()
		b, err := json.Marshal(map[string]any{"prompts": s.prompts, "tool_calls": len(s.byTool), "observed_events": len(s.eng.St.Events), "queue_rejected": s.queueRejected.Load()})
		s.mu.Unlock()
		return nil, string(b), err
	case "AgentStatus":
		var in struct {
			Agent string `json:"agent"`
		}
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return nil, "", err
		}
		if _, ok := adapter.Profiles[in.Agent]; !ok {
			return nil, "", errors.New("알 수 없는 에이전트")
		}
		d.mu.Lock()
		d.ensureProbe(in.Agent)
		version := d.versions[in.Agent]
		caps, tested := adapter.Resolve(in.Agent, version)
		ready, tracked := d.versionReady[in.Agent]
		if !tracked && version != "" {
			ready = true
		}
		b, _ := json.Marshal(map[string]any{"version": version, "ready": ready, "tested": tested, "caps": caps})
		d.mu.Unlock()
		return nil, string(b), nil
	case "Shutdown":
		go d.ln.Close()
		return nil, "bye", nil
	case "Statusline":
		var in struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(req.Payload, &in)
		d.mu.Lock()
		s := d.sessions[in.SessionID]
		d.mu.Unlock()
		if s == nil {
			return nil, "", nil
		}
		return nil, s.Statusline(), nil
	case "Command":
		var in CommandInput
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return nil, "", err
		}
		text, err := d.command(in)
		return nil, text, err
	}
	agent := req.Agent
	if agent == "" {
		agent = "claude"
	}
	events, inputs, err := translateIn(agent, req.Event, req.Payload)
	if err != nil {
		return nil, "", err
	}
	var out json.RawMessage
	claudeEvent := ""
	var deadline time.Time
	if req.DeadlineUnixMs > 0 {
		deadline = time.UnixMilli(req.DeadlineUnixMs)
	}
	for i, ev := range events {
		inputs[i].Deadline = deadline
		inputs[i].Ack = slot
		out, err = d.handleHook(agent, ev, inputs[i])
		if err != nil {
			return nil, "", err
		}
		claudeEvent = ev
	}
	return translateOut(agent, req.Event, claudeEvent, out, adapter.Profiles[agent].Caps.FailClosed), "", nil
}

func (d *Daemon) session(id, agent, root, transcript string) (*Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ended[id] {
		return nil, errors.New("이미 종료된 세션이다")
	}
	if s := d.sessions[id]; s != nil {
		if s.Agent != agent {
			return nil, errors.New("다른 에이전트와 세션 ID가 충돌한다; 새 세션 ID가 필요하다")
		}
		abs, err := filepath.Abs(root)
		if err != nil || filepath.Clean(abs) != filepath.Clean(s.Root) {
			return nil, errors.New("세션 ID가 다른 프로젝트 경로에 재사용되었습니다")
		}
		return s, nil
	}
	ver, ok := d.versions[agent]
	if !ok {
		d.ensureProbe(agent)
	}
	caps, _ := adapter.Resolve(agent, ver)
	s, err := newSession(id, agent, root, transcript, d.db, d.prices, caps)
	if err != nil {
		return nil, err
	}
	d.sessions[id] = s
	return s, nil
}

// ensureProbe requires d.mu. Unknown versions stay observation-only.
func (d *Daemon) ensureProbe(agent string) {
	if _, ok := d.versions[agent]; ok {
		return
	}
	if d.versions == nil {
		d.versions = map[string]string{}
	}
	d.versions[agent] = ""
	if d.versionReady == nil {
		d.versionReady = map[string]bool{}
	}
	d.versionReady[agent] = false
	go d.probeAgent(agent)
}

func (d *Daemon) probeAgent(agent string) {
	probe := d.versionProbe
	if probe == nil {
		probe = adapter.ProbeVersion
	}
	version := probe(agent)
	caps, _ := adapter.Resolve(agent, version)
	d.mu.Lock()
	d.versions[agent] = version
	var sessions []*Session
	for _, s := range d.sessions {
		if s.Agent == agent {
			sessions = append(sessions, s)
		}
	}
	d.mu.Unlock()
	for _, s := range sessions {
		s.mu.Lock()
		if !s.closed {
			s.caps = caps
		}
		s.mu.Unlock()
	}
	d.mu.Lock()
	d.versionReady[agent] = true
	d.mu.Unlock()
}

func (d *Daemon) drop(id string) {
	d.mu.Lock()
	if d.ended == nil {
		d.ended = map[string]bool{}
	}
	d.ended[id] = true
	delete(d.sessions, id)
	d.mu.Unlock()
}

// CommandInput is a slash command request.
type CommandInput struct {
	Name    string `json:"name"`
	Root    string `json:"root"`
	Session string `json:"session"`
	Arg     string `json:"arg"`
}

// latest picks the most recently active session for a project root.
func (d *Daemon) latest(root, id string) *Session {
	d.mu.Lock()
	defer d.mu.Unlock()
	if id != "" {
		if s := d.sessions[id]; s != nil {
			return s
		}
	}
	var cands []*Session
	for _, s := range d.sessions {
		if root == "" || sameDir(s.Root, root) {
			cands = append(cands, s)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].activeAt().After(cands[j].activeAt()) })
	return cands[0]
}

func sameDir(a, b string) bool {
	ca, _ := filepath.Abs(a)
	cb, _ := filepath.Abs(b)
	return ca == cb || strings.HasPrefix(cb, ca+string(filepath.Separator))
}

// command implements the plugin slash commands.
func (d *Daemon) command(in CommandInput) (string, error) {
	s := d.latest(in.Root, in.Session)
	if s == nil {
		return "", errors.New("이 프로젝트에서 삼천포가 보고 있는 세션이 없다")
	}
	s.opMu.RLock()
	defer s.opMu.RUnlock()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed && in.Name != "status" && in.Name != "summary" && in.Name != "card" {
		return "", errors.New("이미 종료된 세션이다")
	}
	switch in.Name {
	case "card":
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.goalCard("").Text(), nil
	case "accept":
		c, raw, err := contract.Load(s.Root)
		if err != nil {
			return "", fmt.Errorf("계약 파일 오류: %w", err)
		}
		if c == nil {
			return "", errors.New(".samcheonpo/contract.yml이 없다")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		acc, err := contract.Accept(s.Root, c, raw, time.Now())
		if err != nil {
			return "", err
		}
		s.setContract(c, acc)
		s.recordStorageError(s.recordIntentCommand(intent.Change{Kind: intent.Confirmed, Goal: c.Goal}))
		if d.db != nil {
			s.recordStorageError(d.db.RecordContract(s.ID, c.ChecksHash(), "accepted"))
		}
		if s.storageErr != nil {
			return "", s.storageErr
		}
		return "계약을 수락했다. 이제 진척은 이 계약의 완료 조건으로 잰다.\n" + strings.TrimSuffix(contract.PlainSummary(c), "\n[/samcheonpo:accept 맞아요]  [/samcheonpo:edit 고칠래요]  [/samcheonpo:skip 계약 없이 진행]"), nil
	case "edit":
		s.mu.Lock()
		defer s.mu.Unlock()
		acc, err := contract.RequestChange(s.Root, in.Arg)
		if err != nil {
			s.recordStorageError(err)
			s.setContract(s.c, contract.Acceptance{State: contract.StateDraft, RequestedGoal: "계약 수정 요청 저장 실패; 재수락 필요"})
			return "", err
		}
		s.setContract(s.c, acc)
		s.recordStorageError(s.recordIntentCommand(intent.Change{Kind: intent.Redirect, Goal: acc.RequestedGoal}))
		if s.storageErr != nil {
			return "", s.storageErr
		}
		return "이전 계약의 검사·범위 승인을 보류했다. 계약 파일은 " + contract.Path(s.Root) + "에 있다. 요청을 반영한 뒤 /samcheonpo:accept로 새 초안을 수락하면 된다. 새 권한·검사 실행은 승인되지 않았다.\n" + s.goalCard("").Text(), nil
	case "skip":
		s.mu.Lock()
		defer s.mu.Unlock()
		acc := contract.Acceptance{State: contract.StateSkipped}
		if err := contract.SaveAcceptance(s.Root, acc); err != nil {
			return "", err
		}
		s.setContract(s.c, acc)
		if d.db != nil {
			s.recordStorageError(d.db.RecordContract(s.ID, "", "skipped"))
		}
		if s.storageErr != nil {
			return "", s.storageErr
		}
		return "계약 없이 진행한다. 진척은 오류 감소와 산출물 증가로만 추정한다.", nil
	case "keep":
		s.mu.Lock()
		v := s.lastPrimary
		if v != nil {
			s.acknowledgeRecovery(v.ID)
		}
		s.unresolved = nil
		s.mu.Unlock()
		if v == nil {
			return "되돌릴 판정이 없다.", nil
		}
		label, reason := "ignore_once", in.Arg
		switch in.Arg {
		case "", "normal", "정상":
			label, reason = "false_positive", "normal"
		case "wrong", "근거":
			label, reason = "false_positive", "wrong_evidence"
		case "now", "지금만":
			label, reason = "ignore_once", "now"
		}
		if err := d.db.AddFeedback(v.ID, s.ID, v.Rule, label, reason, "", s.Root); err != nil {
			return "", err
		}
		s.mu.Lock()
		s.applyOverrides()
		s.eng.Cfg = s.Cfg
		if label == "false_positive" {
			// Released for this goal revision, rule and target only; another
			// target or a new goal is judged afresh.
			s.released[s.adviceKey(*v)] = true
		} else {
			delete(s.advised, s.adviceKey(*v)) // "just this once": advise again before blocking
		}
		s.mu.Unlock()
		target, _ := v.Facts["path"].(string)
		if target == "" {
			target, _ = v.Facts["cmd"].(string)
		}
		scope := "지금 작업 목표 안에서"
		if target != "" {
			scope += " `" + target + "`에 대한"
		}
		if label == "false_positive" {
			return fmt.Sprintf("'%s' 판정을 오탐으로 기록했다. %s 같은 판정은 다시 막지 않는다. 다른 대상이나 새 목표에는 적용되지 않고, 보호 경로와 예산 제한은 그대로다. 같은 판정이 이 프로젝트에서 3번 오탐으로 기록되면 기준을 한 단계 올린다.", receipt.Describe(*v), scope), nil
		}
		return "이번 한 번만 넘어간다. 같은 일이 다시 생기면 막기 전에 먼저 안내한다.", nil
	case "steer":
		s.mu.Lock()
		v := s.lastPrimary
		if v != nil {
			s.acknowledgeRecovery(v.ID)
		}
		ctx := s.msgContext()
		s.mu.Unlock()
		if v == nil {
			return "지금 바꿀 방향이 없다.", nil
		}
		return "아래 내용을 에이전트에게 전달한다.\n\n" + intervene.Agent(*v, ctx, "prescription"), nil
	case "summary":
		return s.plainSummary(), nil
	case "rollback":
		return s.rollback(in.Arg)
	case "check":
		res := s.checkpoint(false)
		s.mu.Lock()
		manual := s.verificationCandidates().ManualPending
		s.mu.Unlock()
		if res == nil {
			if manual > 0 {
				return fmt.Sprintf("자동 실행할 검사는 없다. 수동 완료 조건 %d개는 별도 확인이 필요하다.\n", manual), nil
			}
			return "수락된 계약이 없어 실행할 검사가 없다.", nil
		}
		var b strings.Builder
		for _, r := range res {
			st := "통과"
			switch {
			case r.Skipped != "":
				st = "건너뜀: " + r.Skipped
			case r.Reused:
				st = "기존 통과 근거 재사용: " + r.ReuseReason
			case r.Truncated:
				st = "미확인: 출력 한도를 넘어 완전한 결과를 확보하지 못했다"
			case r.TimedOut:
				st = "시간 초과"
			case !r.Pass:
				st = fmt.Sprintf("실패 (종료 코드 %d)", r.ExitCode)
			}
			if r.SideEffect {
				st += ", 작업 폴더를 바꿔 자동 실행에서 뺐다"
			}
			if !r.Reused && r.ReuseReason != "" {
				st += ", " + r.ReuseReason
			}
			fmt.Fprintf(&b, "%s: %s\n", r.Command, st)
			if r.StopReason != "" && r.Pass && r.Skipped == "" && r.Evidence != nil {
				fmt.Fprintf(&b, "이 검사의 종료 이유: %s (근거 %s)\n", r.StopReason, r.Evidence.ID)
			}
		}
		if manual > 0 {
			fmt.Fprintf(&b, "수동 완료 조건 %d개는 별도 확인이 필요하다. 자동 검사 통과는 전체 목표 완료를 뜻하지 않는다.\n", manual)
		}
		return b.String(), nil
	case "status":
		return s.Statusline(), nil
	}
	return "", fmt.Errorf("알 수 없는 명령 %s", in.Name)
}

func (s *Session) plainSummary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.eng.St
	var b strings.Builder
	if n := s.queueRejected.Load(); n > 0 {
		fmt.Fprintf(&b, "관측 큐 포화로 %d건을 처리하지 못했습니다. 개입과 완료 판정을 중단했으며 이 요약은 불완전합니다.\n", n)
	}
	if s.storageErr != nil {
		b.WriteString("기록 저장에 실패해 이 요약은 불완전할 수 있습니다.\n")
	}
	if s.judgeBudgetReason != "" {
		fmt.Fprintf(&b, "감시 판정: %s\n", s.judgeBudgetReason)
	}
	b.WriteString(s.requestVsNow())
	met, total := s.criteria()
	if total > 0 {
		fmt.Fprintf(&b, "끝난 것: 완료 조건 %d개 중 %d개 충족.\n", total, met)
	} else if len(st.ProgressSeqs) > 0 {
		b.WriteString("끝난 것: 계약이 없어 정확히 잴 수 없지만 파일이 늘고 오류가 줄어든 구간이 있었다.\n")
	} else {
		b.WriteString("끝난 것: 아직 확인된 진척이 없다.\n")
	}
	var blocked []string
	for _, v := range s.eng.Verdicts {
		if v.Primary && v.Level >= detect.L1 {
			blocked = append(blocked, receipt.Describe(v))
		}
	}
	if len(blocked) > 0 {
		if len(blocked) > 3 {
			blocked = blocked[len(blocked)-3:]
		}
		b.WriteString("막힌 것: " + strings.Join(blocked, "; ") + ".\n")
	}
	observedTokens, unpriced, _, _ := s.measuredUsage()
	if observedTokens == 0 {
		b.WriteString("비용·사용량: 미확인(계측 부재는 무료라는 뜻이 아닙니다).\n")
	} else if unpriced > 0 {
		fmt.Fprintf(&b, "계측된 사용량: %d토큰. 단가 누락으로 전체 환산액은 미확인입니다.\n", observedTokens)
	} else {
		fmt.Fprintf(&b, "API 환산액(계측분): %s원, 마지막 진척 이후 %s원. 실제 청구액·절감액은 미확인입니다.\n", contract.Comma(cost.Won(st.TotalMicro)), contract.Comma(cost.Won(st.TotalMicro-st.ProgressMark)))
	}
	next := "남은 일을 한 가지만 골라 그것부터 끝내 달라고 하면 됩니다."
	if s.c != nil && total > met {
		for _, c := range s.c.MachineChecks() {
			if r, ok := s.checks[c.ID]; !ok || !r.Pass {
				next = fmt.Sprintf("다음 지시 추천: \"`%s`가 통과하도록 고쳐 줘. 다른 파일은 건드리지 마.\"", c.Check)
				break
			}
		}
	}
	for i := len(s.recoveries) - 1; i >= 0 && !(total > 0 && met == total); i-- {
		a := s.recoveries[i]
		if a.Revision == s.contractRevision && a.Prescription.Handoff {
			next = "다음 행동: " + a.Prescription.Action + "\n종료 조건: " + a.Prescription.StopCondition
			break
		}
	}
	b.WriteString(next)
	if len(s.rb.agentHash) > 0 {
		b.WriteString("\nAI가 바꾼 파일을 " + s.rollbackBasis() + "으로 되돌리려면 /samcheonpo:rollback 으로 먼저 미리 볼 수 있습니다.")
	}
	return b.String()
}

func readFile(p string) ([]byte, error) { return os.ReadFile(p) }

// requestVsNow is called with s.mu held. It quotes what the user asked for
// next to what the agent is doing now, in plain words, so a non-developer can
// see a drift without reading code.
func (s *Session) requestVsNow() string {
	request := s.firstPrompt
	if s.intentRevision != nil && s.intentRevision.Goal != "" {
		request = s.intentRevision.Goal
	}
	request = strings.Join(strings.Fields(request), " ")
	if r := []rune(request); len(r) > 120 {
		request = string(r[:120]) + "…"
	}
	var b strings.Builder
	if request != "" {
		fmt.Fprintf(&b, "요청한 일: \"%s\"\n", request)
	}
	var files, outside []string
	seen := map[string]bool{}
	var lastCmd string
	evs := s.eng.St.Events
	for i := len(evs) - 1; i >= 0 && len(files) < 5; i-- {
		ev := evs[i]
		if ev.Kind != event.KindTool {
			continue
		}
		if lastCmd == "" && ev.Tool == event.ToolShell && ev.Cmd != "" {
			lastCmd = ev.Cmd
		}
		if ev.Category != event.CatProduce {
			continue
		}
		for _, p := range ev.Paths {
			if seen[p] || len(files) >= 5 {
				continue
			}
			seen[p] = true
			files = append(files, p)
			if in, known := s.eng.InScope(p); known && !in {
				outside = append(outside, p)
			}
		}
	}
	switch {
	case len(files) == 0 && lastCmd == "":
		b.WriteString("지금 하는 일: 아직 파일을 바꾸거나 명령을 실행하지 않았다.\n")
	case len(files) == 0:
		fmt.Fprintf(&b, "지금 하는 일: 파일은 바꾸지 않았고 마지막 명령은 `%s`이다.\n", short(lastCmd, 60))
	default:
		fmt.Fprintf(&b, "지금 하는 일: 최근에 %s를 바꿨다", strings.Join(files, ", "))
		if lastCmd != "" {
			fmt.Fprintf(&b, ", 마지막 명령은 `%s`", short(lastCmd, 60))
		}
		b.WriteString(".\n")
	}
	if len(outside) > 0 {
		fmt.Fprintf(&b, "요청 범위 밖으로 보이는 변경: %s. 의도한 변경이 아니라면 방향을 다시 알려 주면 된다.\n", strings.Join(outside, ", "))
	}
	return b.String()
}

func short(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
