package live

import (
	"encoding/json"
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/notify"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStopReleaseLaterFailureSupersedesUnjudgedPass(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.mu.Lock()
	s.Cfg.Levels.MaxStopHolds = 0
	s.mu.Unlock()
	s.onPrompt(HookInput{Prompt: "로그인 버그를 고쳐 줘"})
	feedWrite(t, s, "w1", "auth/login.go")
	feedCheck(t, s, "t1", "./gradlew build", 0)
	feedCheck(t, s, "t2", "go test ./auth", 1)
	out := string(s.onStop(HookInput{LastMessage: "검사가 실패한 상태에서 중단합니다."}))
	if !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("a later failing check was overridden by an old pass: %s", out)
	}
}

func TestStopReleaseSourceDirectoryNamedTargetNeedsVerification(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "컴파일 타깃 resolver의 버그를 고쳐 줘"})
	feedWrite(t, s, "w1", "src/target/resolve.go")
	out := string(s.onStop(HookInput{LastMessage: "변경 내역을 남겼습니다."}))
	if !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("handwritten source was discarded as tool output: %s", out)
	}
}

func TestStopReleaseNotifiesHostsWithoutUserMessage(t *testing.T) {
	for _, agent := range []string{"agy", "copilot", "mcode", "deepagents"} {
		t.Run(agent, func(t *testing.T) {
			s, _ := reliabilitySession(t)
			messages := make(chan string, 16)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var message map[string]string
				if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				messages <- message["text"]
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			s.mu.Lock()
			s.Agent, s.caps = agent, adapter.Profiles[agent].Caps
			s.Cfg.Notify.Push.Provider, s.Cfg.Notify.Push.URL = "webhook", server.URL
			s.notifier = notify.New(s.Cfg)
			s.mu.Unlock()
			s.onPrompt(HookInput{Prompt: "로그인 기능을 구현해 줘"})
			feedWrite(t, s, "w1", "auth/login.go")
			for i := 0; i < 2; i++ {
				if out := string(s.onStop(HookInput{LastMessage: "변경 내역을 남겼습니다."})); !strings.Contains(out, `"decision":"block"`) {
					t.Fatalf("stop %d not held: %s", i, out)
				}
			}
			raw := s.onStop(HookInput{LastMessage: "변경 내역을 남겼습니다."})
			if strings.Contains(string(raw), `"decision":"block"`) {
				t.Fatalf("bounded stop was not released: %s", raw)
			}
			if agent == "deepagents" {
				var result map[string]any
				if json.Unmarshal(raw, &result) != nil || !strings.Contains(fmt.Sprint(result["completion_notice"]), "미검증") {
					t.Fatalf("SDK completion notice missing: %s", raw)
				}
			}
			deadline := time.NewTimer(2 * time.Second)
			defer deadline.Stop()
			for {
				select {
				case message := <-messages:
					if strings.Contains(message, "미검증 상태로 종료를 허용했습니다") {
						return
					}
				case <-deadline.C:
					t.Fatal("unverified end was not delivered through the configured notification channel")
				}
			}
		})
	}
}
