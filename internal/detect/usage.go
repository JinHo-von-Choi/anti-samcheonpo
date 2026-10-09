package detect

import "github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"

type usagePart struct{ tokens, unpriced, waste, fresh int64 }

type usageCache struct {
	count                         int
	progress                      int64
	parts                         map[*event.Event]usagePart
	dirty                         map[*event.Event]bool
	tokens, unpriced, waste, idle int64
	// fresh is input, output and cache writes: tokens without cache reads,
	// which repeat the same context every turn and dwarf the rest.
	fresh int64
}

func (s *State) invalidateUsage(ev *event.Event) {
	if s.usageCache.parts == nil {
		return
	}
	if s.usageCache.dirty == nil {
		s.usageCache.dirty = map[*event.Event]bool{}
	}
	s.usageCache.dirty[ev] = true
}

// UsageSummary incrementally incorporates appended observations and invalidated
// late usage/waste classifications. Caller must serialize with engine mutation.
// Unchanged reads are O(1); changing the progress boundary recomputes idle tokens.
func (s *State) UsageSummary() (tokens, unpriced, waste, idle int64) {
	c := s.refreshUsage()
	return c.tokens, c.unpriced, c.waste, c.idle
}

// FreshTokens is the session's input, output and cache-write tokens, the
// measure session-length limits use. Same serialization as UsageSummary.
func (s *State) FreshTokens() int64 { return s.refreshUsage().fresh }

func (s *State) refreshUsage() *usageCache {
	c := &s.usageCache
	if c.parts == nil || c.count > len(s.Events) {
		*c = usageCache{parts: map[*event.Event]usagePart{}, progress: s.LastProgress}
	}
	update := func(ev *event.Event) {
		old := c.parts[ev]
		next := usagePart{tokens: ev.Usage.Total(), fresh: ev.Usage.In + ev.Usage.Out + ev.Usage.CacheWrite}
		if !ev.Priced {
			next.unpriced = next.tokens
		}
		if _, ok := s.Wasted[ev.Seq]; ok {
			next.waste = ev.CostMicroKRW
		}
		c.tokens += next.tokens - old.tokens
		c.fresh += next.fresh - old.fresh
		c.unpriced += next.unpriced - old.unpriced
		c.waste += next.waste - old.waste
		if ev.Seq > c.progress {
			c.idle += next.tokens - old.tokens
		}
		c.parts[ev] = next
	}
	for _, ev := range s.Events[c.count:] {
		update(ev)
	}
	c.count = len(s.Events)
	for ev := range c.dirty {
		if _, observed := c.parts[ev]; observed {
			update(ev)
		}
		delete(c.dirty, ev)
	}
	if c.progress != s.LastProgress {
		c.idle = 0
		for ev, part := range c.parts {
			if ev.Seq > s.LastProgress {
				c.idle += part.tokens
			}
		}
		c.progress = s.LastProgress
	}
	return c
}
