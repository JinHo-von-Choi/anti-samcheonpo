package live

import (
	"context"
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/assessment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"time"
)

type linkedTrigger struct {
	Seq    int64
	Result string
}

// scheduleLinked only updates bounded triggers under s.mu. I/O and throttling
// happen in one background worker; arrivals during a read are not discarded.
func (s *Session) scheduleLinked(ev *event.Event) {
	if s.closed || s.ledgerPath == "" || s.task == nil || s.intentRevision == nil {
		return
	}
	if ev.Mutating || ev.Unknown || ev.Category == event.CatProduce {
		s.linkedPending = nil
		s.linkedGeneration++
		s.linkedSummary = nil
		return
	}
	if ev.BehaviorTarget == "" || ev.BehaviorResult == "" || ev.Reliability == nil || !ev.Reliability.Complete {
		return
	}
	if s.linkedPending == nil {
		s.linkedPending = map[string]linkedTrigger{}
	}
	if len(s.linkedPending) >= 128 {
		if _, exists := s.linkedPending[ev.BehaviorTarget]; !exists {
			for key := range s.linkedPending {
				delete(s.linkedPending, key)
				break
			}
		}
	}
	s.linkedPending[ev.BehaviorTarget] = linkedTrigger{ev.Seq, ev.BehaviorResult}
	s.linkedGeneration++
	s.startLinkedLocked()
}

func (s *Session) startLinkedLocked() {
	if s.closed || s.linkedBusy || len(s.linkedPending) == 0 || s.task == nil || s.intentRevision == nil {
		return
	}
	task, revision, path := s.task.ID, s.intentRevision.Number, s.ledgerPath
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.linkedCancel, s.linkedDone, s.linkedBusy = cancel, done, true
	go func() {
		var processed uint64
		defer close(done)
		defer cancel()
		defer func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.linkedBusy = false
			// Preserve arrivals during cancellation or the final unlock.
			if s.linkedGeneration != processed || ctx.Err() != nil {
				s.startLinkedLocked()
			}
		}()
		for {
			s.mu.Lock()
			delay := max(time.Duration(0), 2*time.Second-time.Since(s.linkedAt))
			s.mu.Unlock()
			if delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			if ctx.Err() != nil {
				return
			}
			s.mu.Lock()
			generation := s.linkedGeneration
			s.linkedAt = time.Now()
			s.mu.Unlock()
			readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
			db, err := ledger.OpenReadOnly(path)
			var nodes []assessment.TaskNode
			var truncated bool
			var summary assessment.LinkedSummary
			if err == nil {
				nodes, truncated, err = db.LinkedWindow(readCtx, task, revision, 32, 256)
				db.Close()
			}
			readCancel()
			if err == nil {
				summary, err = assessment.LinkedTask(nodes)
				summary.WindowTruncated, summary.UsageScope = truncated, "observed_window"
				if truncated && len(nodes) == 0 {
					summary.State, summary.Cycles = "window_incomplete", nil
				}
			}
			s.mu.Lock()
			if ctx.Err() != nil || s.closed || s.task == nil || s.intentRevision == nil || s.task.ID != task || s.intentRevision.Number != revision {
				s.mu.Unlock()
				return
			}
			s.linkedUnavailable = err != nil
			if err == nil {
				s.publishLinkedLocked(summary, task, revision)
			}
			processed = generation
			again := generation != s.linkedGeneration && len(s.linkedPending) > 0
			s.mu.Unlock()
			if !again {
				return
			}
		}
	}()
}

func (s *Session) publishLinkedLocked(summary assessment.LinkedSummary, task string, revision uint64) {
	s.linkedSummary = &summary
	scope := fp.Hash(task, stringRevision(revision))
	if s.linkedSeen == nil || s.linkedScope != scope {
		s.linkedSeen, s.linkedScope = map[string]bool{}, scope
	}
	for _, cycle := range summary.Cycles {
		trigger, ok := s.linkedPending[cycle.Target]
		if !ok || trigger.Result != cycle.Result || s.linkedSeen[cycle.KeyHash] {
			continue
		}
		local := false
		for _, ref := range cycle.Evidence {
			if ref.Agent == s.Agent && ref.SessionID == s.ID && ref.Seq == trigger.Seq {
				local = true
			}
		}
		if !local {
			continue
		}
		if len(s.linkedSeen) >= 128 {
			for key := range s.linkedSeen {
				delete(s.linkedSeen, key)
				break
			}
		}
		s.linkedSeen[cycle.KeyHash] = true
		ev := &event.Event{Kind: event.KindTool, Tool: "linked_monitor", RawTool: "samcheonpo.linked", Category: event.CatWatch, TS: time.Now(), Basis: "live", Priced: true, Summary: "linked repeat candidate"}
		s.parser.AddEvent(ev)
		s.eng.ObserveWatch(ev)
		s.persistEvent(ev)
		sig := detect.Signal{Detector: "S1", Rule: "s1.linked_loop", Level: detect.L1, Confidence: .65, Estimate: true, Evidence: []int64{trigger.Seq}, Facts: map[string]any{"candidate_key": cycle.KeyHash, "scope_hash": scope, "target_hash": cycle.Target, "key_hash": cycle.KeyHash, "linked_evidence": cycle.Evidence, "handoff_return": cycle.HandoffReturn, "count": cycle.Count, "sessions": len(cycle.Sessions)}}
		s.deliver(s.eng.Emit(ev, []detect.Signal{sig}))
	}
	s.broadcastHUD()
}
func stringRevision(r uint64) string { return fmt.Sprint(r) }

func (s *Session) restoreLinkedSeen() {
	if s.task == nil || s.intentRevision == nil {
		return
	}
	s.linkedScope = fp.Hash(s.task.ID, stringRevision(s.intentRevision.Number))
	s.linkedSeen = map[string]bool{}
	for i := len(s.eng.Verdicts) - 1; i >= 0 && len(s.linkedSeen) < 128; i-- {
		v := s.eng.Verdicts[i]
		if v.Rule != "s1.linked_loop" {
			continue
		}
		scope, _ := v.Facts["scope_hash"].(string)
		key, _ := v.Facts["key_hash"].(string)
		if scope == s.linkedScope && key != "" {
			s.linkedSeen[key] = true
		}
	}
}
