package live

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
)

func TestShutdownDoesNotWaitForIdleHUDReader(t *testing.T) {
	h := startDaemon(t)
	h.send("SessionStart", map[string]any{"source": "startup"})
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/a.py 고쳐 줘"})
	conn, err := net.DialTimeout("unix", SocketPath(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req, _ := json.Marshal(map[string]any{"v": 1, "event": "HUDStream", "payload": map[string]any{"session_id": "sess-1"}})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(conn).ReadBytes('\n'); err != nil {
		t.Fatal(err)
	}
	hookclient.Query("Shutdown", map[string]string{}, time.Second)
	select {
	case err := <-h.done:
		h.done <- err
	case <-time.After(3 * time.Second):
		t.Fatal("the daemon kept running because a HUD reader was connected and idle")
	}
}
