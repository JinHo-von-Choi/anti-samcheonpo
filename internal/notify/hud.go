package notify

import (
	"sync"
	"time"
)

// HUDPayloadType tags HUD state payloads so a stream can route them apart
// from other event kinds.
const HUDPayloadType = "hud.state"

// hudChannelBuffer is the per-subscriber buffer. It holds one payload so a
// subscriber that polls slightly late still gets the newest state.
const hudChannelBuffer = 1

// HUDState is the non-developer facing snapshot rendered by the intervention
// HUD: what is happening, how much it costs, and what to press.
type HUDState struct {
	// StatusText is the short verdict shown to the user, e.g. "순조로움",
	// "지켜보는 중", "지금 끼어드세요", "확인 불가".
	StatusText string `json:"status_text"`
	// WasteKRW is the money burnt on futile turns, in Korean won.
	WasteKRW int64 `json:"waste_krw"`
	// WastePercentage is WasteKRW against total spend, 0 to 100.
	WastePercentage int `json:"waste_percentage"`
	// RecommendedAction is the one sentence the user should act on.
	RecommendedAction string `json:"recommended_action"`
	// PresetCommands are one-click commands covering the recommendation.
	PresetCommands []string `json:"preset_commands"`
	// IsBlocked reports whether execution is halted pending approval.
	IsBlocked bool `json:"is_blocked"`
}

// HUDPayload is the JSON serialised form broadcast to HUD subscribers.
type HUDPayload struct {
	Type string `json:"type"`
	// GeneratedAt is the build time in Unix milliseconds.
	GeneratedAt int64    `json:"generated_at"`
	State       HUDState `json:"state"`
}

// BuildStateUpdate turns a HUDState into a broadcast payload. Metrics are
// clamped to their documented ranges and the command list is copied, so a
// payload read from several goroutines never aliases caller owned memory.
func BuildStateUpdate(state HUDState) HUDPayload {
	if state.WastePercentage < 0 {
		state.WastePercentage = 0
	}
	if state.WastePercentage > 100 {
		state.WastePercentage = 100
	}
	if state.WasteKRW < 0 {
		state.WasteKRW = 0
	}
	commands := make([]string, len(state.PresetCommands))
	copy(commands, state.PresetCommands)
	state.PresetCommands = commands

	return HUDPayload{
		Type:        HUDPayloadType,
		GeneratedAt: time.Now().UnixMilli(),
		State:       state,
	}
}

// HUDManager owns the subscriber set for HUD state broadcasts.
type HUDManager struct {
	mu   sync.RWMutex
	subs map[chan HUDPayload]struct{}
	last HUDPayload
}

// NewHUDManager creates a manager with no subscribers and no state.
func NewHUDManager() *HUDManager {
	return &HUDManager{subs: make(map[chan HUDPayload]struct{})}
}

// Subscribe registers a subscriber and returns its payload channel together
// with a cancel function. The cancel function is idempotent and closes the
// channel.
func (m *HUDManager) Subscribe() (<-chan HUDPayload, func()) {
	ch := make(chan HUDPayload, hudChannelBuffer)

	m.mu.Lock()
	m.subs[ch] = struct{}{}
	m.mu.Unlock()

	return ch, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if _, ok := m.subs[ch]; !ok {
			return
		}
		delete(m.subs, ch)
		close(ch)
	}
}

// Broadcast publishes a state to every subscriber and returns the payload it
// sent. A subscriber that is not reading keeps only its newest payload, so a
// stalled reader can never block the broadcaster.
func (m *HUDManager) Broadcast(state HUDState) HUDPayload {
	payload := BuildStateUpdate(state)

	m.mu.Lock()
	m.last = payload
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()
	for ch := range m.subs {
		deliverLatest(ch, payload)
	}
	return payload
}

// Current returns the most recently broadcast state, or the zero value when
// nothing has been broadcast yet.
func (m *HUDManager) Current() HUDState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.last.State
}

// deliverLatest writes payload into ch, replacing an unread payload so the
// subscriber always observes the newest state.
func deliverLatest(ch chan HUDPayload, payload HUDPayload) {
	select {
	case ch <- payload:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- payload:
	default:
	}
}
