package cost

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestMissingUsageAndMeasuredZeroAreDifferent(t *testing.T) {
	missing := Measure(ObserveSession(&event.Session{}, "prices"), 0, 0, 0)
	if missing.ObservedTokens != nil || missing.APIEquivalentMicro != nil || len(missing.Missing) == 0 {
		t.Fatalf("missing became zero: %+v", missing)
	}
	zero := Measure(ObserveSession(&event.Session{UsageLines: 1, UsageParsed: 1}, "prices"), 0, 0, 0)
	if zero.ObservedTokens == nil || *zero.ObservedTokens != 0 || zero.APIEquivalentMicro == nil || *zero.APIEquivalentMicro != 0 {
		t.Fatalf("known zero lost: %+v", zero)
	}
}

func TestSubscriptionAndQuotaCannotEstablishCashSavings(t *testing.T) {
	if o := ObserveSession(&event.Session{QuotaWindowMin: 300}, "prices"); o.Quota != nil {
		t.Fatal("missing percent became zero")
	}
	o := ObserveSession(&event.Session{QuotaKnown: true, QuotaUsedPct: 0, QuotaWindowMin: 300, UsageLines: 1, UsageParsed: 1}, "prices")
	if o.Kind != BillingUnknown || o.Quota == nil || o.Quota.UsedPct != 0 {
		t.Fatalf("quota inferred billing: %+v", o)
	}
	o.Kind = BillingSubscription
	o.KindSource = "user_declared"
	m := Measure(o, 5000000, 1000, 0)
	if m.APIEquivalentMicro == nil || *m.APIEquivalentMicro != 5000000 || m.ActualChargeMicro != nil || m.VerifiedSavingsMicro != nil {
		t.Fatalf("equivalence became cash: %+v", m)
	}
	partial := Measure(o, 5000000, 1000, 1)
	if partial.APIEquivalentMicro != nil || partial.ObservedTokens == nil {
		t.Fatal("missing price became complete amount")
	}
}

func TestInvalidQuotaAndMissingUsageCoverageRemainExplicit(t *testing.T) {
	o := Observation{Quota: &Quota{UsedPct: math.NaN(), WindowMinutes: 300}}
	m := Measure(o, 10, 10, 0)
	if m.Quota != nil || len(m.Missing) == 0 || m.UsageComplete {
		t.Fatalf("%+v", m)
	}
	if _, err := json.Marshal(m); err != nil {
		t.Fatal("invalid numeric escaped into JSON", err)
	}
	if !math.IsNaN(o.Quota.UsedPct) {
		t.Fatal("caller observation mutated")
	}
}
