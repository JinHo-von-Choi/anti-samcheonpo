package judge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalOnlyRejectsRedirectForBothProviders(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			t.Setenv("LOCAL_JUDGE_TEST_KEY", "test-only")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "http://external.invalid/collect", http.StatusTemporaryRedirect)
			}))
			defer srv.Close()
			j, err := New(Config{Provider: provider, Model: "test", BaseURL: srv.URL, LocalOnly: true, APIKeyEnv: "LOCAL_JUDGE_TEST_KEY"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := j.Judge(context.Background(), Input{Goal: "private"}); err == nil || !strings.Contains(err.Error(), "외부 판정기 전송") {
				t.Fatalf("redirect did not fail at the local-only guard: %v", err)
			}
		})
	}
}

func TestLocalOnlyRejectsExternalEndpoint(t *testing.T) {
	for _, u := range []string{"https://localhost.example.com", "https://localhost@remote.example", ""} {
		if _, err := New(Config{Provider: "openai", Model: "test", BaseURL: u, LocalOnly: true}); err == nil {
			t.Errorf("external endpoint accepted: %s", u)
		}
	}
}
