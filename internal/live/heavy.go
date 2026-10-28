package live

import (
	"sync"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// heavyRun is a heavy test run one session started in a workspace.
type heavyRun struct {
	session string
	callID  string
	cmd     string
	started time.Time
	expect  time.Duration // how long the same tests took last time (0: unknown)
	// background marks a run whose launch already answered; its result
	// arrives later and releases it
	background bool
}

// heavyHold is how long a run without a result is still taken to be
// running: twice its last duration, at least 30 minutes. Past it the run is
// unknown and judges nothing, so a lost result never holds a workspace.
func (r *heavyRun) live(now time.Time) bool {
	limit := 30 * time.Minute
	if d := 2 * r.expect; d > limit {
		limit = d
	}
	return now.Sub(r.started) <= limit
}

// heavyRegistry holds the heavy test runs per workspace across the daemon's
// sessions. A checkpoint check takes a place in it like a tool call.
type heavyRegistry struct {
	mu   sync.Mutex
	runs map[string][]*heavyRun // workspace key -> runs
}

// conflict returns the oldest run still running in the workspace that is
// not this call's own: a run of another session, or a background run of the
// same session.
func (h *heavyRegistry) conflict(key, session string, now time.Time) *heavyRun {
	h.mu.Lock()
	defer h.mu.Unlock()
	var oldest *heavyRun
	for _, r := range h.runs[key] {
		if !r.live(now) || r.session == session && !r.background {
			continue
		}
		if oldest == nil || r.started.Before(oldest.started) {
			oldest = r
		}
	}
	if oldest == nil {
		return nil
	}
	c := *oldest
	return &c
}

// hold records a run. Runs long past their hold are dropped on the way.
func (h *heavyRegistry) hold(key string, r *heavyRun) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runs == nil {
		h.runs = map[string][]*heavyRun{}
	}
	var kept []*heavyRun
	for _, x := range h.runs[key] {
		if x.live(r.started) || r.started.Sub(x.started) < 2*time.Hour {
			kept = append(kept, x)
		}
	}
	h.runs[key] = append(kept, r)
}

// release drops a call's run. A call that moved to the background keeps it
// until its result.
func (h *heavyRegistry) release(key, session, callID string, background bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	runs := h.runs[key]
	for i, r := range runs {
		if r.session != session || r.callID != callID {
			continue
		}
		if background {
			r.background = true
			return
		}
		h.runs[key] = append(runs[:i:i], runs[i+1:]...)
		return
	}
}

// releaseSession drops every run of an ending session.
func (h *heavyRegistry) releaseSession(key, session string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.runs[key]) == 0 {
		return
	}
	var kept []*heavyRun
	for _, r := range h.runs[key] {
		if r.session != session {
			kept = append(kept, r)
		}
	}
	h.runs[key] = kept
}

// heavyKey is the workspace a session's heavy runs compete in: the git
// directory (each worktree has its own), else the session root.
func (s *Session) heavyKey() string {
	if s.ws == nil {
		return s.Root
	}
	return s.ws.Key()
}

// heavyPre judges a heavy test run that would start while another one runs
// in the same workspace. The caller holds s.mu.
func (s *Session) heavyPre(ev *event.Event) *detect.Signal {
	if s.daemon == nil {
		return nil
	}
	heavy, last := s.eng.HeavyCheck(ev)
	if !heavy {
		return nil
	}
	now := time.Now()
	other := s.daemon.heavy.conflict(s.heavyKey(), s.ID, now)
	if other == nil {
		return nil
	}
	facts := map[string]any{"cmd": ev.CmdNorm, "other_cmd": other.cmd, "other": shortID(other.session),
		"since": int(now.Sub(other.started).Minutes()), "target": other.cmd}
	if other.session == s.ID {
		facts["other"] = "이 세션의 백그라운드 실행"
	}
	if other.expect > 0 {
		facts["expect"] = int(other.expect.Minutes() + 0.5)
	}
	if last > 0 {
		facts["last_minutes"] = int(last.Minutes() + 0.5)
	}
	return &detect.Signal{Detector: "S1", Rule: "s1.concurrent_heavy_check", Confidence: 0.85, Level: detect.L1,
		Evidence: []int64{ev.Seq}, Facts: facts}
}

// holdHeavy records a heavy test run that is about to start. The caller
// holds s.mu.
func (s *Session) holdHeavy(ev *event.Event) {
	if s.daemon == nil {
		return
	}
	heavy, last := s.eng.HeavyCheck(ev)
	if !heavy {
		return
	}
	s.daemon.heavy.hold(s.heavyKey(), &heavyRun{session: s.ID, callID: ev.CallID, cmd: ev.CmdNorm, started: time.Now(), expect: last})
}

// releaseHeavy releases a call's run once its result is known; background
// keeps it until the background result.
func (s *Session) releaseHeavy(callID string, background bool) {
	if s.daemon == nil {
		return
	}
	s.daemon.heavy.release(s.heavyKey(), s.ID, callID, background)
}

// heavyBusy reports a heavy run of another session in the workspace, so the
// contract checkpoint waits instead of competing with it.
func (s *Session) heavyBusy() bool {
	if s.daemon == nil {
		return false
	}
	return s.daemon.heavy.conflict(s.heavyKey(), s.ID, time.Now()) != nil
}

// holdCheckpoint takes a place for a contract check the daemon runs itself.
// Its expected time is unknown, so it holds for the 30-minute minimum at
// most; the returned function releases it.
func (s *Session) holdCheckpoint(command string) func() {
	if s.daemon == nil {
		return func() {}
	}
	key := s.heavyKey()
	id := "checkpoint:" + command + ":" + time.Now().Format(time.RFC3339Nano)
	s.daemon.heavy.hold(key, &heavyRun{session: s.ID, callID: id, cmd: command, started: time.Now(), background: true})
	return func() { s.daemon.heavy.release(key, s.ID, id, false) }
}

// joinsBackground reports an agent whose background results complete the
// start (package bgrun); for the others a background run ends unobserved.
func (s *Session) joinsBackground() bool { return s.Agent == "mcode" }

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
