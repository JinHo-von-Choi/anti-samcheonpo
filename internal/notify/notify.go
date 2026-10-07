// Package notify delivers user notifications: desktop, ntfy and a generic webhook.
package notify

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
)

// Notifier sends notifications according to configuration.
type Notifier struct {
	desktop  bool
	provider string
	topic    string
	url      string
	client   *http.Client
	// Sent records delivered messages (used by tests and the bench).
	Sent []string
}

// New creates a notifier.
func New(cfg config.Config) *Notifier {
	return &Notifier{desktop: cfg.Notify.Desktop, provider: cfg.Notify.Push.Provider, topic: cfg.Notify.Push.Topic, url: cfg.Notify.Push.URL,
		client: &http.Client{Timeout: 5 * time.Second}}
}

// Send delivers a message asynchronously; failures are ignored so a broken
// notification channel never blocks the agent.
func (n *Notifier) Send(title, body string) {
	if n == nil {
		return
	}
	n.Sent = append(n.Sent, body)
	go n.send(title, body)
}

func (n *Notifier) send(title, body string) {
	if n.desktop {
		switch runtime.GOOS {
		case "linux":
			if p, err := exec.LookPath("notify-send"); err == nil {
				_ = exec.Command(p, "-a", "samcheonpo", title, body).Run()
			}
		case "darwin":
			script := `display notification "` + esc(body) + `" with title "` + esc(title) + `"`
			_ = exec.Command("osascript", "-e", script).Run()
		}
	}
	switch n.provider {
	case "ntfy":
		if n.topic == "" {
			return
		}
		base := n.url
		if base == "" {
			base = "https://ntfy.sh"
		}
		req, err := http.NewRequest("POST", strings.TrimSuffix(base, "/")+"/"+n.topic, strings.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Title", title)
		if resp, err := n.client.Do(req); err == nil {
			resp.Body.Close()
		}
	case "webhook":
		if n.url == "" {
			return
		}
		b, _ := json.Marshal(map[string]string{"title": title, "text": body})
		if resp, err := n.client.Post(n.url, "application/json", bytes.NewReader(b)); err == nil {
			resp.Body.Close()
		}
	}
}

func esc(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) }
