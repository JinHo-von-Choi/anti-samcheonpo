package codex

import (
	"encoding/json"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestQuotaRequiresBothFieldsAndAcceptsExplicitZero(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		known bool
	}{
		{`{"primary":{"window_minutes":300}}`, false},
		{`{"primary":{"used_percent":0}}`, false},
		{`{"primary":{"used_percent":0,"window_minutes":300}}`, true},
		{`{"primary":{"used_percent":101,"window_minutes":300}}`, false},
		{`{"primary":{"used_percent":12.5,"window_minutes":0}}`, false},
		{`null`, false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			p := &Parser{sess: &event.Session{QuotaKnown: true, QuotaUsedPct: 80, QuotaWindowMin: 300}}
			var rl any
			if err := json.Unmarshal([]byte(tc.raw), &rl); err != nil {
				t.Fatal(err)
			}
			p.tokenCount(map[string]any{"rate_limits": rl})
			if p.sess.QuotaKnown != tc.known {
				t.Fatalf("quota known = %v", p.sess.QuotaKnown)
			}
			if !tc.known && p.sess.QuotaWindowMin != 0 {
				t.Fatal("retained stale quota")
			}
			if tc.known && p.sess.QuotaUsedPct != 0 {
				t.Fatal("explicit zero lost")
			}
		})
	}
}

func TestTrackerQuotaUpdatesWithoutNewTokens(t *testing.T) {
	tk := &Tracker{last: tokenUsage{Total: 10}}
	for _, info := range []string{`null`, `{"total_token_usage":{"total_tokens":10}}`} {
		line := []byte(`{"payload":{"type":"token_count","info":` + info + `,"rate_limits":{"primary":{"used_percent":0,"window_minutes":300}}}}`)
		if _, ok := tk.feed(line); ok {
			t.Fatal("duplicate usage")
		}
		if tk.Quota == nil || tk.Quota.UsedPct != 0 {
			t.Fatal("quota update lost")
		}
	}
	tk.feed([]byte(`{"payload":{"type":"token_count","rate_limits":{"primary":{"window_minutes":300}}}}`))
	if tk.Quota != nil {
		t.Fatal("missing rate is not zero or stale rate")
	}
}
