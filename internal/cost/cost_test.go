package cost

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestBuiltinTable(t *testing.T) {
	tb, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range tb.Models {
		if m.Source == "" || m.Input <= 0 || m.Output <= 0 {
			t.Errorf("row %+v", m)
		}
	}
	if p, ok := tb.Lookup("claude-opus-5-5", time.Now()); !ok || p.Input != 4 {
		t.Errorf("exact lookup %+v %v", p, ok)
	}
	// longest prefix wins: a dated id resolves to its family, not claude-opus-5
	if p, ok := tb.Lookup("claude-opus-5-5-20270101", time.Now()); !ok || p.Model != "claude-opus-5-5" {
		t.Errorf("prefix lookup %+v", p)
	}
	if _, ok := tb.Lookup("mystery-model-1", time.Now()); ok {
		t.Error("unknown model must be unpriced")
	}
	if _, ok := tb.Lookup("<synthetic>", time.Now()); ok {
		t.Error("synthetic model must be unpriced")
	}
}

func TestMicroKRW(t *testing.T) {
	tb, _ := Load("")
	fx := tb.Rate(time.Now())
	u := event.Usage{In: 1_000_000, Model: "claude-sonnet-5-5"}
	got, ok := tb.MicroKRW(u, time.Now())
	if !ok || got != int64(2*fx*1_000_000) {
		t.Errorf("1M input tokens of sonnet-5-5 = %d micro-won, want %d", got, int64(2*fx*1_000_000))
	}
	// 1-hour cache writes cost 2x input; 5-minute ones use the cache_write price
	five, _ := tb.MicroKRW(event.Usage{CacheWrite: 1_000_000, Model: "claude-opus-5-5"}, time.Now())
	hour, _ := tb.MicroKRW(event.Usage{CacheWrite: 1_000_000, CacheWrite1h: 1_000_000, Model: "claude-opus-5-5"}, time.Now())
	if five != int64(5*fx*1e6) || hour != int64(8*fx*1e6) {
		t.Errorf("cache write 5m=%d 1h=%d", five, hour)
	}
	if _, ok := tb.MicroKRW(event.Usage{In: 10, Model: "unknown"}, time.Now()); ok {
		t.Error("unpriced usage must report ok=false (never 0 won)")
	}
}

func TestUserFXOverride(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "prices.yml")
	if err := os.WriteFile(p, []byte("fx:\n  - usd_krw: 1500\n    valid_from: 2026-09-01\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tb, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if r := tb.Rate(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)); r != 1385 {
		t.Errorf("rate before override %v", r)
	}
	if r := tb.Rate(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)); r != 1500 {
		t.Errorf("rate after override %v", r)
	}
	if err := os.WriteFile(p, []byte("models:\n  - model: x\n    input: 1\n    output: 1\n    valid_from: 2026-01-01\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Error("a price row without source url must be rejected")
	}
}

func TestRounding(t *testing.T) {
	lines := []int64{333_333, 333_333, 333_334} // 1 won in total
	got := RoundTo(lines, Won(1_000_000))
	var sum int64
	for _, v := range got {
		sum += v
	}
	if sum != 1 {
		t.Errorf("rounded lines %v must sum to the rounded total", got)
	}
	if Won(1_499_999) != 1 || Won(1_500_000) != 2 || Won(-1_500_000) != -2 {
		t.Error("half-up rounding")
	}
}
