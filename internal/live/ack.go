package live

// ackSlot holds what one hook response delivers to the agent: advice keys
// whose repetition may later be blocked, and escalated blocks that count
// against the per-session cap. Nothing is recorded until the hook client
// confirms it printed the response. A response the client never received,
// because it arrived after the client's deadline or the output failed, leaves
// no advice history and spends no block.
type ackSlot struct {
	s           *Session
	keys        []string
	escalations int
}

func (a *ackSlot) pending() bool {
	return a != nil && a.s != nil && (len(a.keys) > 0 || a.escalations > 0)
}

// noteAdvice is called with s.mu held. Without a transport slot (direct calls
// inside the daemon) the advice is recorded at once, as before.
func (s *Session) noteAdvice(a *ackSlot, key string) {
	if a == nil {
		s.advised[key] = true
		return
	}
	a.s = s
	a.keys = append(a.keys, key)
}

// noteEscalation is called with s.mu held.
func (s *Session) noteEscalation(a *ackSlot) {
	if a == nil {
		s.escalations++
		return
	}
	a.s = s
	a.escalations++
}

// confirmAck records a printed response.
func (a *ackSlot) confirm() {
	if !a.pending() {
		return
	}
	s := a.s
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range a.keys {
		s.advised[k] = true
	}
	s.escalations += a.escalations
}
