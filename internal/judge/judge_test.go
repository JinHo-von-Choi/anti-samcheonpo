package judge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseLabel(t *testing.T) {
	cases := map[string]string{"on_track": OnTrack, "Drift.": Drift, "I think side_quest": SideQuest, "maybe": ""}
	for in, want := range cases {
		if got := ParseLabel(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestPromptHasNoRawCode(t *testing.T) {
	p := Prompt(Input{Goal: "로그인 만료 처리", Recent: []string{"edit src/auth/session.ts", "shell: npm test"}})
	if !strings.Contains(p, "로그인 만료 처리") || !strings.Contains(p, "edit src/auth/session.ts") {
		t.Fatal(p)
	}
}

func TestOpenAICompatible(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "bad", 400)
			return
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"drift"}}],"usage":{"prompt_tokens":120,"completion_tokens":2}}`))
	}))
	defer srv.Close()
	t.Setenv("JUDGE_KEY", "k")
	j, err := New(Config{Provider: "openai", Model: "local-small", BaseURL: srv.URL + "/v1", APIKeyEnv: "JUDGE_KEY", LocalOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	v, err := j.Judge(context.Background(), Input{Goal: "g", Recent: []string{"x"}})
	if err != nil || v.Label != Drift || v.Usage.In != 120 || v.Usage.Out != 2 || v.Usage.Model != "local-small" {
		t.Fatalf("verdict %+v %v", v, err)
	}
	if got["model"] != "local-small" || got["max_tokens"].(float64) != 16 {
		t.Errorf("request %v", got)
	}
}

func TestAnthropicSDK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != "k2" {
			http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`, 400)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["model"] != "claude-haiku-4-5" {
			http.Error(w, "model", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-4-5","content":[{"type":"text","text":"on_track"}],
			"stop_reason":"end_turn","usage":{"input_tokens":200,"output_tokens":3,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}`))
	}))
	defer srv.Close()
	t.Setenv("JUDGE_KEY2", "k2")
	j, err := New(Config{Provider: "anthropic", BaseURL: srv.URL, APIKeyEnv: "JUDGE_KEY2", LocalOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	v, err := j.Judge(context.Background(), Input{Goal: "g"})
	if err != nil || v.Label != OnTrack || v.Usage.In != 200 || v.Usage.Model != "claude-haiku-4-5" {
		t.Fatalf("verdict %+v %v", v, err)
	}
}

func TestUnknownProvider(t *testing.T) {
	if _, err := New(Config{Provider: "x"}); err == nil {
		t.Error("unknown provider")
	}
	if _, err := New(Config{Provider: "openai"}); err == nil {
		t.Error("openai provider needs base_url and model")
	}
}
