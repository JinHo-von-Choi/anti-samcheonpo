package live

import (
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestSaturatedQueueReturnsWithoutWaitingForWorker(t *testing.T) {
	s := &Session{queue: make(chan *job, 1)}
	s.enqueue(&event.Event{}, 0)
	done := make(chan struct{})
	go func() {
		s.enqueue(&event.Event{}, time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		// Release a broken blocking implementation before failing the test.
		<-s.queue
		t.Fatal("saturated enqueue waited for worker")
	}
	if s.queueRejected.Load() != 1 || len(s.queue) != 1 {
		t.Fatal("overflow was hidden or expanded the bounded queue")
	}
	s.queueMu.Lock()
	s.queueClosed = true
	close(s.queue)
	s.queueMu.Unlock()
	s.enqueue(&event.Event{}, 0)
	if s.queueRejected.Load() != 1 {
		t.Fatal("late closed-session request counted as saturation")
	}
}

func TestQueueGapPreventsReceiptAndSurvivesRestart(t *testing.T) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "true")
	s.mu.Lock()
	baseline := len(s.parser.Session.Events)
	// Hold the worker at its session lock while filling its bounded queue.
	for i := 0; i < 300; i++ {
		ev := &event.Event{Kind: event.KindMessage, TS: time.Now(), Priced: true}
		s.parser.AddEvent(ev)
		s.enqueue(ev, 0)
	}
	s.mu.Unlock()
	rejected := s.queueRejected.Load()
	if rejected == 0 {
		t.Fatal("test did not saturate queue")
	}
	if !strings.Contains(s.Statusline(), "미처리") || !strings.Contains(s.plainSummary(), "불완전") {
		t.Fatal("missing observations not visible")
	}
	if results := s.checkpoint(false); len(results) != 0 {
		t.Fatal("incomplete evidence still ran automatic checks")
	}
	if path, err := s.Finalize(); err == nil || path != "" || !strings.Contains(err.Error(), "큐 포화") {
		t.Fatalf("incomplete stream produced receipt: %q %v", path, err)
	}
	if got, err := s.db.ObservationGap(s.Agent, s.ID); err != nil || got != rejected {
		t.Fatalf("gap not saved: %d %v", got, err)
	}
	var stored int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM event WHERE session_id=?", s.ID).Scan(&stored); err != nil || stored != baseline+300-int(rejected) {
		t.Fatalf("admitted events not drained: %d %v", stored, err)
	}
	restarted, err := newSession(s.ID, s.Agent, s.Root, "", s.db, s.prices, adapter.Profiles[s.Agent].Caps)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Finalize()
	if restarted.queueRejected.Load() != rejected {
		t.Fatal("restart cleared evidence gap")
	}
	if _, err := restarted.Finalize(); err == nil {
		t.Fatal("restart manufactured complete receipt")
	}
}
