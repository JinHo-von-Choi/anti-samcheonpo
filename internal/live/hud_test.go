package live

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/notify"
)

// The HUD and the status line come from one state: a reader that polls gets
// the same verdict the status line shows, and a subscriber sees the finding
// with its one-press commands as soon as it is delivered.
func TestHUDMirrorsStatuslineAndStreamsFindings(t *testing.T) {
	h := startDaemon(t)
	h.send("SessionStart", map[string]any{"source": "startup"})
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/a.py 시험 통과시켜 줘"})

	conn, err := net.DialTimeout("unix", SocketPath(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req, _ := json.Marshal(map[string]any{"v": 1, "event": "HUDStream", "payload": map[string]any{"session_id": "sess-1"}})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		t.Fatal(err)
	}
	rd := bufio.NewReader(conn)
	next := func() notify.HUDPayload {
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		line, err := rd.ReadBytes('\n')
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		var p notify.HUDPayload
		if err := json.Unmarshal(line, &p); err != nil {
			t.Fatalf("stream line: %v %q", err, line)
		}
		return p
	}
	first := next()
	if first.Type != notify.HUDPayloadType || first.State.StatusText == "" || !first.State.UsageUnknown {
		t.Fatalf("the stream opens with the current state: %+v", first)
	}
	text, _, ok := hookclient.Query("Statusline", map[string]string{"session_id": "sess-1"}, 2*time.Second)
	if !ok || text != "["+first.State.StatusText+"] "+first.State.Summary {
		t.Fatalf("the status line renders the same state: %q vs %+v", text, first.State)
	}

	var found notify.HUDPayload
	for i := 0; i < 6 && found.State.Observation == ""; i++ {
		h.shell(fmt.Sprintf("r%d", i), "pytest -q", "FAILED tests/test_a.py::test_x - assert 1 == 2\n1 failed", 1)
		deadline := time.Now().Add(3 * time.Second)
		for found.State.Observation == "" && time.Now().Before(deadline) {
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			line, err := rd.ReadBytes('\n')
			if err != nil {
				break
			}
			var p notify.HUDPayload
			if json.Unmarshal(line, &p) == nil && p.State.Observation != "" {
				found = p
			}
		}
	}
	if found.State.Observation == "" || found.State.RecommendedAction == "" || len(found.State.PresetCommands) != 4 {
		t.Fatalf("a delivered finding reaches subscribers with its sentence and commands: %+v", found.State)
	}
	if !strings.Contains(found.State.Requested, "src/a.py") {
		t.Fatalf("the HUD quotes the request: %+v", found.State)
	}
	out, err := hookclient.QueryOutput("HUD", map[string]string{"root": h.proj}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var p notify.HUDPayload
	if err := json.Unmarshal(out, &p); err != nil || p.State.Observation == "" {
		t.Fatalf("a poll returns the same current state: %v %s", err, out)
	}
}
