package notify

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func sampleHUDState() HUDState {
	return HUDState{
		StatusText:        "지금 끼어드세요",
		WasteKRW:          12800,
		WastePercentage:   37,
		RecommendedAction: "실행을 멈추고 마지막 커밋으로 되돌리세요",
		PresetCommands:    []string{"/samcheonpo:steer", "/samcheonpo:rollback pass"},
		IsBlocked:         true,
	}
}

func recvHUD(t *testing.T, ch <-chan HUDPayload) HUDPayload {
	t.Helper()
	select {
	case p, ok := <-ch:
		if !ok {
			t.Fatal("subscriber channel closed before a payload arrived")
		}
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a HUD payload")
	}
	return HUDPayload{}
}

func TestBuildStateUpdatePreservesState(t *testing.T) {
	state := sampleHUDState()

	got := BuildStateUpdate(state)

	if got.Type != HUDPayloadType {
		t.Fatalf("type = %q, want %q", got.Type, HUDPayloadType)
	}
	if got.GeneratedAt <= 0 {
		t.Fatalf("generated_at = %d, want a positive unix millisecond stamp", got.GeneratedAt)
	}
	if !reflect.DeepEqual(got.State, state) {
		t.Fatalf("state = %+v, want %+v", got.State, state)
	}
}

func TestBuildStateUpdateJSONShape(t *testing.T) {
	state := sampleHUDState()

	raw, err := json.Marshal(BuildStateUpdate(state))
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	for _, key := range []string{
		`"type"`, `"generated_at"`, `"state"`, `"status_text"`, `"waste_krw"`,
		`"waste_percentage"`, `"recommended_action"`, `"preset_commands"`, `"is_blocked"`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("payload JSON is missing %s: %s", key, raw)
		}
	}

	var decoded HUDPayload
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded.Type != HUDPayloadType {
		t.Fatalf("decoded type = %q, want %q", decoded.Type, HUDPayloadType)
	}
	if !reflect.DeepEqual(decoded.State, state) {
		t.Fatalf("decoded state = %+v, want %+v", decoded.State, state)
	}
}

func TestBuildStateUpdateClampsWasteMetrics(t *testing.T) {
	cases := []struct {
		name    string
		in      HUDState
		wantPct int
		wantKRW int64
	}{
		{"percentage above 100", HUDState{WastePercentage: 150, WasteKRW: 5000}, 100, 5000},
		{"negative percentage", HUDState{WastePercentage: -8, WasteKRW: 5000}, 0, 5000},
		{"negative waste", HUDState{WastePercentage: 12, WasteKRW: -900}, 12, 0},
		{"boundary values kept", HUDState{WastePercentage: 100, WasteKRW: 0}, 100, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildStateUpdate(tc.in)
			if got.State.WastePercentage != tc.wantPct {
				t.Fatalf("waste_percentage = %d, want %d", got.State.WastePercentage, tc.wantPct)
			}
			if got.State.WasteKRW != tc.wantKRW {
				t.Fatalf("waste_krw = %d, want %d", got.State.WasteKRW, tc.wantKRW)
			}
		})
	}
}

func TestBuildStateUpdateEncodesEmptyPresetCommandsAsArray(t *testing.T) {
	got := BuildStateUpdate(HUDState{StatusText: "순조로움"})

	if got.State.PresetCommands == nil {
		t.Fatal("preset_commands must be a non-nil slice")
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if !strings.Contains(string(raw), `"preset_commands":[]`) {
		t.Fatalf("empty preset commands must encode as [], got %s", raw)
	}
}

func TestBuildStateUpdateCopiesPresetCommands(t *testing.T) {
	cmds := []string{"/samcheonpo:steer"}

	got := BuildStateUpdate(HUDState{PresetCommands: cmds})
	cmds[0] = "/mutated-afterwards"

	if got.State.PresetCommands[0] != "/samcheonpo:steer" {
		t.Fatalf("payload aliases the caller slice: %v", got.State.PresetCommands)
	}
}

func TestNewHUDManagerStartsEmpty(t *testing.T) {
	m := NewHUDManager()
	if m == nil {
		t.Fatal("NewHUDManager returned nil")
	}
	if got := m.Current(); !reflect.DeepEqual(got, HUDState{}) {
		t.Fatalf("current state before any broadcast = %+v, want zero value", got)
	}
}

func TestHUDManagerBroadcastReachesSubscriber(t *testing.T) {
	m := NewHUDManager()
	ch, cancel := m.Subscribe()
	defer cancel()

	state := sampleHUDState()
	sent := m.Broadcast(state)
	got := recvHUD(t, ch)

	if !reflect.DeepEqual(got, sent) {
		t.Fatalf("delivered payload = %+v, want %+v", got, sent)
	}
	if !reflect.DeepEqual(got.State, state) {
		t.Fatalf("delivered state = %+v, want %+v", got.State, state)
	}
	if !reflect.DeepEqual(m.Current(), state) {
		t.Fatalf("current state = %+v, want %+v", m.Current(), state)
	}
}

func TestHUDManagerBroadcastReachesAllSubscribers(t *testing.T) {
	m := NewHUDManager()

	var chans []<-chan HUDPayload
	var cancels []func()
	for i := 0; i < 3; i++ {
		ch, cancel := m.Subscribe()
		chans = append(chans, ch)
		cancels = append(cancels, cancel)
	}
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()

	state := sampleHUDState()
	m.Broadcast(state)

	for i, ch := range chans {
		if got := recvHUD(t, ch); !reflect.DeepEqual(got.State, state) {
			t.Fatalf("subscriber %d got %+v, want %+v", i, got.State, state)
		}
	}
}

func TestHUDManagerCancelClosesChannelAndStopsDelivery(t *testing.T) {
	m := NewHUDManager()
	ch, cancel := m.Subscribe()

	m.Broadcast(sampleHUDState())
	recvHUD(t, ch)

	cancel()
	cancel()

	m.Broadcast(HUDState{StatusText: "확인 불가"})

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("subscriber channel stayed open after cancel")
		}
	}
}

func TestHUDManagerKeepsLatestPayloadForSilentSubscriber(t *testing.T) {
	m := NewHUDManager()
	ch, cancel := m.Subscribe()
	defer cancel()

	for i := 1; i <= 5; i++ {
		m.Broadcast(HUDState{
			StatusText: "지켜보는 중",
			WasteKRW:   int64(i * 1000),
			IsBlocked:  i%2 == 0,
		})
	}

	got := recvHUD(t, ch)
	if got.State.WasteKRW != 5000 {
		t.Fatalf("silent subscriber kept waste_krw = %d, want the latest value 5000", got.State.WasteKRW)
	}
}

func TestHUDManagerBroadcastDoesNotBlockWithoutReaders(t *testing.T) {
	m := NewHUDManager()
	_, cancel := m.Subscribe()
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			m.Broadcast(sampleHUDState())
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("broadcast blocked while no subscriber was reading")
	}
}

func TestHUDManagerConcurrentBroadcastAndSubscribe(t *testing.T) {
	m := NewHUDManager()

	stop := make(chan struct{})
	observed := make(chan struct{})
	go func() {
		defer close(observed)
		for {
			select {
			case <-stop:
				return
			default:
				_ = m.Current()
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				m.Broadcast(sampleHUDState())
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				ch, cancel := m.Subscribe()
				select {
				case <-ch:
				case <-time.After(20 * time.Millisecond):
				}
				cancel()
			}
		}()
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = BuildStateUpdate(HUDState{
					StatusText:      "순조로움",
					WastePercentage: j,
					PresetCommands:  []string{"/samcheonpo:steer " + strconv.Itoa(j)},
				})
			}
		}()
	}

	wg.Wait()
	close(stop)
	<-observed
}
