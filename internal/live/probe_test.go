package live

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
)

func TestSlowVersionProbeDoesNotHoldDaemonOrLoseDraft(t *testing.T) {
	s, d := reliabilitySession(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	d.versions = map[string]string{}
	d.versionProbe = func(string) string { close(started); <-release; return "2.1.0" }
	done := make(chan *Session, 1)
	errors := make(chan error, 1)
	go func() {
		next, err := d.session("cold", "claude", s.Root, "")
		if err != nil {
			errors <- err
			return
		}
		done <- next
	}()
	var next *Session
	select {
	case next = <-done:
	case err := <-errors:
		unblock()
		t.Fatal(err)
	case <-time.After(time.Second):
		unblock()
		t.Fatal("probe blocked session creation")
	}
	t.Cleanup(func() { _, _ = next.Finalize() })
	next.mu.Lock()
	next.Cfg.Contract.Draft = "on" // this test is about the draft surviving the probe
	next.mu.Unlock()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("probe not started")
	}
	next.mu.Lock()
	initialCaps := next.caps
	next.mu.Unlock()
	if initialCaps.BlockPre {
		t.Fatal("unconfirmed version gained blocking")
	}
	out, _, err := d.handle(Request{V: ProtocolVersion, Event: "Ping"})
	if err != nil || out != nil {
		t.Fatal("daemon blocked by probe")
	}
	next.onPrompt(HookInput{Prompt: "cold task"})
	next.mu.Lock()
	pending := len(next.pendingDraft)
	next.mu.Unlock()
	if pending == 0 {
		t.Fatal("draft discarded before capability confirmation")
	}
	next.onPrompt(HookInput{Prompt: "목표 변경: latest cold goal"})
	unblock()
	ready := false
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		_, text, err := d.handle(Request{Event: "AgentStatus", Payload: json.RawMessage(`{"agent":"claude"}`)})
		var state struct {
			Ready  bool
			Tested bool
		}
		if err == nil && json.Unmarshal([]byte(text), &state) == nil && state.Ready && state.Tested {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("probe did not publish verified capability")
	}
	next.mu.Lock()
	finalCaps := next.caps
	next.mu.Unlock()
	if finalCaps != adapter.Profiles["claude"].Caps {
		t.Fatal("capability not applied")
	}
	message, _ := next.takePendingFor("PreToolUse", nil)
	if !strings.Contains(message, "contract.yml") || !strings.Contains(message, "latest cold goal") || strings.Contains(message, "cold task") {
		t.Fatal("first draft not delivered on supported hook", message)
	}
}

// A version probe that read nothing (a slow start on a busy host) does not
// leave the agent observation-only until the daemon restarts: the next hook
// after probeRetry probes again and applies the capabilities.
func TestFailedVersionProbeIsRetried(t *testing.T) {
	_, d := reliabilitySession(t)
	old := probeRetry
	t.Cleanup(func() { probeRetry = old })
	var mu sync.Mutex
	answer := ""
	d.versions = map[string]string{}
	d.versionProbe = func(string) string { mu.Lock(); defer mu.Unlock(); return answer }
	status := func() (ready, tested bool) {
		_, text, err := d.handle(Request{Event: "AgentStatus", Payload: json.RawMessage(`{"agent":"claude"}`)})
		var state struct{ Ready, Tested bool }
		if err != nil || json.Unmarshal([]byte(text), &state) != nil {
			t.Fatal(err)
		}
		return state.Ready, state.Tested
	}
	for deadline := time.Now().Add(time.Second); ; time.Sleep(time.Millisecond) {
		if ready, _ := status(); ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first probe did not finish")
		}
	}
	if _, tested := status(); tested {
		t.Fatal("an empty version is not a tested one")
	}
	d.mu.Lock()
	probeRetry = 0 // the retry interval has passed
	d.mu.Unlock()
	mu.Lock()
	answer = "2.1.0"
	mu.Unlock()
	for deadline := time.Now().Add(time.Second); ; time.Sleep(time.Millisecond) {
		if _, tested := status(); tested {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a failed probe was never retried")
		}
	}
}
