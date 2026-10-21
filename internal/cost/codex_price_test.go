package cost

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"testing"
	"time"
)

func TestAstraPriceProvenanceAndEffectiveDate(t *testing.T) {
	table, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	before := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	if _, ok := table.Lookup("gpt-6-astra", before); ok {
		t.Fatal("unverified historical price backdated")
	}
	after := before.Add(24 * time.Hour)
	p, ok := table.Lookup("gpt-6-astra", after)
	if !ok || p.Input != 10 || p.Output != 50 || p.CacheRead != 1 || p.CacheWrite != 12.5 || p.CheckedAt != "2026-10-21" || p.Source == "" || p.Basis == "" {
		t.Fatalf("price provenance missing: %+v", p)
	}
	u := event.Usage{In: 100, Model: "gpt-6-astra"}
	u.Add(event.Usage{In: 100, Model: "gpt-5.5"})
	if _, ok := table.MicroKRW(u, after); ok {
		t.Fatal("mixed models charged at first model rate")
	}
}
