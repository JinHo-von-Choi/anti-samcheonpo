package verification

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentChecksShareExecutionAndChangedInputInvalidates(t *testing.T) {
	c := NewCache(2)
	e := passingEvidence(time.Now())
	var calls atomic.Int32
	start := make(chan struct{})
	release := make(chan struct{})
	run := func(context.Context) (Evidence, error) {
		if calls.Add(1) == 1 {
			close(start)
		}
		<-release
		return e, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, _, err := c.Do(context.Background(), e.Key, run)
			if err != nil || got.ID != e.ID {
				t.Errorf("%+v %v", got, err)
			}
		}()
	}
	<-start
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("duplicated execution", calls.Load())
	}
	if _, reused, err := c.Do(context.Background(), e.Key, run); err != nil || !reused {
		t.Fatal("passing check not reused")
	}
	e.Key.InputHash = "changed"
	if _, reused, err := c.Do(context.Background(), e.Key, run); err != nil || reused {
		t.Fatal("changed input reused")
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
}

func TestFailuresPartialResultsAndCanceledWaiter(t *testing.T) {
	c := NewCache(1)
	e := passingEvidence(time.Now())
	e.Pass = false
	e.ExitCode = 1
	var calls int
	run := func(context.Context) (Evidence, error) { calls++; return e, nil }
	for i := 0; i < 2; i++ {
		if _, reused, err := c.Do(context.Background(), e.Key, run); err != nil || reused {
			t.Fatal("failure cached")
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	e.Pass = true
	e.ExitCode = 0
	e.Complete = false
	for i := 0; i < 2; i++ {
		if _, reused, err := c.Do(context.Background(), e.Key, run); err != nil || reused {
			t.Fatal("partial result cached")
		}
	}
	start := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Do(context.Background(), e.Key, func(context.Context) (Evidence, error) { close(start); <-release; return e, nil })
	}()
	<-start
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.Do(ctx, e.Key, run); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	<-done
}

func TestCacheBoundedAndForget(t *testing.T) {
	c := NewCache(1)
	e := passingEvidence(time.Now())
	run := func(context.Context) (Evidence, error) { return e, nil }
	c.Do(context.Background(), e.Key, run)
	e.Key.CheckID = "second"
	c.Do(context.Background(), e.Key, run)
	if len(c.entries) != 1 {
		t.Fatal("unbounded cache")
	}
	c.Forget(e.Key.TaskID)
	if len(c.entries) != 0 {
		t.Fatal("forget retained evidence")
	}
}
