package verification

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type flight struct {
	done     chan struct{}
	evidence Evidence
	err      error
}

// Cache stores only reusable passes. Simultaneous requests for the same key
// share one observation (including failure); a later failure request retries.
// It is bounded and local. Persisted evidence must be revalidated on import.
type Cache struct {
	mu       sync.Mutex
	capacity int
	entries  map[Key]Evidence
	flights  map[Key]*flight
}

func NewCache(capacity int) *Cache {
	if capacity < 1 {
		capacity = 256
	}
	return &Cache{capacity: capacity, entries: map[Key]Evidence{}, flights: map[Key]*flight{}}
}

func (c *Cache) Do(ctx context.Context, key Key, execute func(context.Context) (Evidence, error)) (Evidence, bool, error) {
	if err := ctx.Err(); err != nil {
		return Evidence{}, false, err
	}
	if !key.Valid() {
		e, err := execute(ctx)
		return e, false, err
	}
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		if reusable, _ := Reusable(e, key, time.Now()); reusable {
			c.mu.Unlock()
			return e, true, nil
		}
		delete(c.entries, key)
	}
	if pending, ok := c.flights[key]; ok {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return Evidence{}, false, ctx.Err()
		case <-pending.done:
			return pending.evidence, true, pending.err
		}
	}
	pending := &flight{done: make(chan struct{})}
	c.flights[key] = pending
	c.mu.Unlock()
	// Always release waiters, including an executor panic. The owner still
	// receives the panic: a programming bug is not a successful check.
	defer func() {
		if p := recover(); p != nil {
			c.mu.Lock()
			pending.err = fmt.Errorf("verification executor panicked")
			delete(c.flights, key)
			close(pending.done)
			c.mu.Unlock()
			panic(p)
		}
	}()
	e, err := execute(ctx)
	c.mu.Lock()
	pending.evidence, pending.err = e, err
	if err == nil && ctx.Err() == nil {
		if reusable, _ := Reusable(e, key, time.Now()); reusable {
			if len(c.entries) >= c.capacity {
				var oldest Key
				var at time.Time
				for k, v := range c.entries {
					if at.IsZero() || v.ObservedAt.Before(at) {
						oldest, at = k, v.ObservedAt
					}
				}
				delete(c.entries, oldest)
			}
			c.entries[key] = e
		}
	}
	delete(c.flights, key)
	close(pending.done)
	c.mu.Unlock()
	return e, false, err
}

// Forget removes stored passes for a task. In-flight results still carry their
// original revision; consumers must compare it to the currently accepted one.
func (c *Cache) Forget(taskID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if k.TaskID == taskID {
			delete(c.entries, k)
		}
	}
}
