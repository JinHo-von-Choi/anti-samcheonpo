package live

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/judge"
)

type budgetTestJudge struct {
	calls int
	fail  bool
}

func (j *budgetTestJudge) Judge(context.Context, judge.Input) (judge.Verdict, error) {
	j.calls++
	if j.fail {
		return judge.Verdict{}, errors.New("transport failed")
	}
	return judge.Verdict{Label: judge.OnTrack, Usage: event.Usage{In: 10, Out: 1, Model: "claude-haiku-4-5"}}, nil
}

func budgetSession(t *testing.T) (*Session, *budgetTestJudge) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "true")
	j := &budgetTestJudge{}
	s.mu.Lock()
	s.judge = j
	s.Cfg.Detectors.S3.Judge.Model = "claude-haiku-4-5"
	s.Cfg.Detectors.S3.MaxWatchCostRatio = .5
	s.eng.St.TotalMicro = 1000000000
	s.mu.Unlock()
	return s, j
}

func TestJudgeBudgetRejectsBeforeCallAndDeduplicates(t *testing.T) {
	s, j := budgetSession(t)
	s.Cfg.Detectors.S3.MaxWatchKRW = 1
	s.runJudge()
	if j.calls != 0 {
		t.Fatal("reservation over cap dispatched")
	}
	s.Cfg.Detectors.S3.MaxWatchKRW = 1000
	s.runJudge()
	s.runJudge()
	if j.calls != 1 || s.judgeBudget.Calls != 1 {
		t.Fatal("duplicate calls", j.calls)
	}
}

func TestJudgeFailureCostIsUnknownAndNotRetried(t *testing.T) {
	s, j := budgetSession(t)
	j.fail = true
	s.runJudge()
	s.runJudge()
	if j.calls != 1 || !s.judgeBudget.Unknown || s.judgeBudget.ReservedMicro == 0 {
		t.Fatal("failure was free retry")
	}
	if !strings.Contains(s.plainSummary(), "미확인") {
		t.Fatal("failure cost hidden")
	}
	s.mu.Lock()
	o := cost.ObserveSession(s.parser.Session, s.prices.Version)
	s.mu.Unlock()
	if o.UsageComplete || !strings.Contains(strings.Join(o.Missing, " "), "감시 판정 비용 미확인") {
		t.Fatalf("%+v", o)
	}
}

type deadlineBudgetJudge struct{ calls int }

func (j *deadlineBudgetJudge) Judge(ctx context.Context, _ judge.Input) (judge.Verdict, error) {
	j.calls++
	<-ctx.Done()
	return judge.Verdict{}, ctx.Err()
}

func TestJudgeDeadlineStopsWithoutAutomaticRetry(t *testing.T) {
	s, _ := budgetSession(t)
	j := &deadlineBudgetJudge{}
	s.judge = j
	s.Cfg.Detectors.S3.JudgeTimeoutSec = 1
	s.runJudge()
	s.runJudge()
	if j.calls != 1 || !s.judgeBudget.Unknown || s.judgeBudget.Pending {
		t.Fatalf("calls=%d budget=%+v", j.calls, s.judgeBudget)
	}
}

func TestJudgeFinalizationAndRestartKeepUnknownReservation(t *testing.T) {
	s, _ := budgetSession(t)
	j := pausedIntentJudge{started: make(chan struct{}), release: make(chan struct{})}
	s.judge = j
	done := make(chan struct{})
	go func() { s.runJudge(); close(done) }()
	select {
	case <-j.started:
	case <-time.After(2 * time.Second):
		close(j.release)
		t.Fatal("judge did not start")
	}
	if _, err := s.Finalize(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("finalization did not cancel judge")
	}
	b, err := s.db.JudgeBudget(s.Agent, s.ID)
	if err != nil || !b.Unknown || b.Pending || b.ReservedMicro == 0 {
		t.Fatalf("%+v %v", b, err)
	}
	resumed, err := newSession(s.ID, s.Agent, s.Root, "", s.db, s.prices, s.caps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = resumed.Finalize() })
	probe := &budgetTestJudge{}
	resumed.judge = probe
	resumed.Cfg.Detectors.S3.Judge.Model = "claude-haiku-4-5"
	resumed.eng.St.TotalMicro = 1000000000
	resumed.runJudge()
	if probe.calls != 0 || !resumed.judgeBudget.Unknown || resumed.judgeBudget.Calls != 1 {
		t.Fatal("restart reset unsettled charge")
	}
}

func TestJudgeReservationStorageFailurePreventsDispatch(t *testing.T) {
	s, j := budgetSession(t)
	s.db.Close()
	s.runJudge()
	if j.calls != 0 || s.storageErr == nil {
		t.Fatal("API called before durable reservation")
	}
}
