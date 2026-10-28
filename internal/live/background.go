package live

import (
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/mcode"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/bgrun"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// backgroundRun is a shell call started without waiting for its end.
type backgroundRun struct {
	start   *event.Event
	endedAt time.Time // when the agent stated the task ended, if it did
}

// backgroundLimit bounds the background calls kept for a join. Past it the
// oldest is closed as unknown, so a session that never reads its tasks
// cannot grow this without bound.
const backgroundLimit = 256

// trackBackground keeps a background start until its final result. The
// caller holds s.mu.
func (s *Session) trackBackground(ev *event.Event, taskID string) {
	if taskID == "" {
		return
	}
	if s.background == nil {
		s.background = map[string]*backgroundRun{}
	}
	s.background[taskID] = &backgroundRun{start: ev}
	if len(s.background) <= backgroundLimit {
		return
	}
	oldest, at := "", int64(0)
	for id, r := range s.background {
		if oldest == "" || r.start.Seq < at {
			oldest, at = id, r.start.Seq
		}
	}
	s.missBackground(oldest, "background_limit")
}

// missBackground closes a background call whose result never arrived. The
// start event keeps the gap on record instead of passing for a result. The
// caller holds s.mu.
func (s *Session) missBackground(id, code string) {
	r := s.background[id]
	if r == nil {
		return
	}
	delete(s.background, id)
	s.releaseHeavy(r.start.CallID, false)
	r.start.Decisions = append(r.start.Decisions, event.Decision{Rule: "observation.collection", Code: code, Evidence: []int64{r.start.Seq}})
	if r.start.Reliability != nil {
		r.start.Reliability.SourceMissing = true
	}
	s.persistEvent(r.start)
}

// closeBackground closes every background call still waiting at session end.
func (s *Session) closeBackground() {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.background))
	for id := range s.background {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s.missBackground(id, "background_result_missing")
	}
}

// taskEndRe reads a task end the harness states in a notice:
// <task task_id="bg_…" status="failed" … ended_at_ms="1793191830608"/>.
var taskEndRe = regexp.MustCompile(`<task task_id="([^"]+)"[^>]*\bended_at_ms="(\d+)"`)

// noteBackgroundEnds records the end times a harness notice states, so a
// result read later is not timed by when it was read.
func (s *Session) noteBackgroundEnds(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range taskEndRe.FindAllStringSubmatch(text, -1) {
		r := s.background[m[1]]
		if r == nil {
			continue
		}
		if ms, err := strconv.ParseInt(m[2], 10, 64); err == nil && ms > 0 {
			r.endedAt = time.UnixMilli(ms)
		}
	}
}

// onBackgroundResult completes a background call once its task reached a
// final status (see package bgrun).
func (s *Session) onBackgroundResult(in HookInput) {
	e := in.Mcode
	if e == nil || e.Result.TaskID == "" || !mcode.Terminal(e.TaskState) {
		return
	}
	s.mu.Lock()
	r := s.background[e.Result.TaskID]
	if r == nil {
		// unknown task, or its result was already taken
		s.mu.Unlock()
		return
	}
	delete(s.background, e.Result.TaskID)
	s.releaseHeavy(r.start.CallID, false)
	res := e.Result
	ev := bgrun.Completion(r.start, bgrun.Outcome{State: res.State, Failure: res.Failure, Exit: res.Exit, Text: res.Text, TaskID: res.TaskID, Source: "background_result"}, r.endedAt, time.Now())
	ev.Basis = "live"
	s.parser.AddEvent(ev)
	s.noteVerify(ev)
	s.mu.Unlock()
	s.enqueue(ev, postWait)
}

// restoreBackground rebuilds the waiting background calls from stored
// events after a daemon restart: a start without a stored completion is
// still running as far as anything recorded says.
func (s *Session) restoreBackground(evs []*event.Event) {
	done := map[int64]bool{}
	for _, ev := range evs {
		if ev.OriginSeq > 0 {
			done[ev.OriginSeq] = true
		}
	}
	for _, ev := range evs {
		if !ev.Background || ev.Execution == nil || ev.Execution.ProcessID == "" || done[ev.Seq] {
			continue
		}
		missed := false
		for _, d := range ev.Decisions {
			if d.Rule == "observation.collection" && (d.Code == "background_result_missing" || d.Code == "background_limit") {
				missed = true
			}
		}
		if !missed {
			s.trackBackground(ev, ev.Execution.ProcessID)
		}
	}
}
