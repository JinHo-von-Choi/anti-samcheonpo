// Package judge implements the optional S3 semantic drift judgement (plan
// 2.3). It is off by default; the input is an event summary, never raw code
// or prompts.
package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// Labels returned by a judge.
const (
	OnTrack   = "on_track"
	SideQuest = "side_quest"
	Drift     = "drift"
)

// Input is what a judge sees.
type Input struct {
	Goal   string
	Recent []string // event summaries (paths and normalized commands)
}

// Verdict is a judgement and its token usage.
type Verdict struct {
	Label string
	Usage event.Usage
}

// Judge classifies recent activity against the task goal.
type Judge interface {
	Judge(ctx context.Context, in Input) (Verdict, error)
}

// Config selects a provider.
type Config struct {
	Provider  string `yaml:"provider" json:"provider"` // anthropic | openai
	Model     string `yaml:"model" json:"model"`
	BaseURL   string `yaml:"base_url" json:"base_url"`
	APIKeyEnv string `yaml:"api_key_env" json:"api_key_env"`
	LocalOnly bool   `yaml:"-" json:"-"`
}

// IsLocalEndpoint accepts only HTTP endpoints on explicit loopback hosts.
func IsLocalEndpoint(base string) bool {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type localTransport struct{ base *http.Transport }

func (t localTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !IsLocalEndpoint(req.URL.String()) {
		return nil, fmt.Errorf("외부 판정기 전송이 허용되지 않았다")
	}
	return t.base.RoundTrip(req)
}

func judgeHTTPClient(localOnly bool) *http.Client {
	c := &http.Client{Timeout: 60 * time.Second}
	if localOnly {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = nil
		dialer := &net.Dialer{Timeout: 30 * time.Second}
		tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if strings.EqualFold(host, "localhost") {
				host = "127.0.0.1"
			}
			ip := net.ParseIP(host)
			if ip == nil || !ip.IsLoopback() {
				return nil, fmt.Errorf("판정기는 루프백 주소에만 연결할 수 있다")
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		}
		// Every request, including redirects and SDK retries, passes this guard.
		c.Transport = localTransport{base: tr}
	}
	return c
}

// EffectiveModel shares the existing provider default with cost reservation.
func EffectiveModel(provider, configured string) string {
	if configured == "" && provider == "anthropic" {
		return "claude-haiku-4-5"
	}
	return configured
}

// New builds a judge, or returns an error when it cannot be configured.
func New(c Config) (Judge, error) {
	if c.LocalOnly && !IsLocalEndpoint(c.BaseURL) {
		return nil, fmt.Errorf("외부 판정기 전송이 허용되지 않았다")
	}
	httpClient := judgeHTTPClient(c.LocalOnly)
	key := ""
	if c.APIKeyEnv != "" {
		key = os.Getenv(c.APIKeyEnv)
	}
	c.Model = EffectiveModel(c.Provider, c.Model)
	switch c.Provider {
	case "anthropic":
		opts := []option.RequestOption{option.WithMaxRetries(0), option.WithRequestTimeout(30 * time.Second), option.WithHTTPClient(httpClient)}
		if key != "" {
			opts = append(opts, option.WithAPIKey(key))
		}
		if c.BaseURL != "" {
			opts = append(opts, option.WithBaseURL(c.BaseURL))
		}
		return &anthropicJudge{client: anthropic.NewClient(opts...), model: c.Model}, nil
	case "openai":
		if c.BaseURL == "" || c.Model == "" {
			return nil, fmt.Errorf("openai 호환 판정기는 base_url과 model이 필요하다")
		}
		return &openAIJudge{url: strings.TrimSuffix(c.BaseURL, "/") + "/chat/completions", model: c.Model, key: key,
			http: httpClient}, nil
	}
	return nil, fmt.Errorf("알 수 없는 판정기 제공자 %q", c.Provider)
}

// Prompt renders the classification request.
func Prompt(in Input) string {
	var b strings.Builder
	b.WriteString("You classify whether a coding agent's recent activity serves the user's task.\n")
	b.WriteString("Task goal: " + in.Goal + "\n")
	b.WriteString("Recent activity (newest last):\n")
	for _, r := range in.Recent {
		b.WriteString("- " + r + "\n")
	}
	b.WriteString("Answer with exactly one word: on_track (directly advancing the goal), side_quest (related but not needed), or drift (unrelated to the goal).")
	return b.String()
}

// ParseLabel extracts a label from model text.
func ParseLabel(s string) string {
	t := strings.ToLower(s)
	for _, l := range []string{Drift, SideQuest, OnTrack} {
		if strings.Contains(t, l) {
			return l
		}
	}
	return ""
}

type anthropicJudge struct {
	client anthropic.Client
	model  string
}

func (j *anthropicJudge) Judge(ctx context.Context, in Input) (Verdict, error) {
	resp, err := j.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(j.model),
		MaxTokens: 16,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(Prompt(in)))},
	})
	if err != nil {
		return Verdict{}, err
	}
	var text strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	u := event.Usage{In: resp.Usage.InputTokens, Out: resp.Usage.OutputTokens, CacheRead: resp.Usage.CacheReadInputTokens,
		CacheWrite: resp.Usage.CacheCreationInputTokens, Model: j.model}
	return Verdict{Label: ParseLabel(text.String()), Usage: u}, nil
}

type openAIJudge struct {
	url, model, key string
	http            *http.Client
}

func (j *openAIJudge) Judge(ctx context.Context, in Input) (Verdict, error) {
	body, _ := json.Marshal(map[string]any{
		"model":       j.model,
		"max_tokens":  16,
		"temperature": 0,
		"messages":    []map[string]string{{"role": "user", "content": Prompt(in)}},
	})
	req, err := http.NewRequestWithContext(ctx, "POST", j.url, bytes.NewReader(body))
	if err != nil {
		return Verdict{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if j.key != "" {
		req.Header.Set("Authorization", "Bearer "+j.key)
	}
	resp, err := j.http.Do(req)
	if err != nil {
		return Verdict{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Verdict{}, fmt.Errorf("판정기 응답 %s", resp.Status)
	}
	var r struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Prompt     int64 `json:"prompt_tokens"`
			Completion int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return Verdict{}, err
	}
	if len(r.Choices) == 0 {
		return Verdict{}, fmt.Errorf("판정기 응답에 선택지가 없다")
	}
	return Verdict{Label: ParseLabel(r.Choices[0].Message.Content), Usage: event.Usage{In: r.Usage.Prompt, Out: r.Usage.Completion, Model: j.model}}, nil
}
