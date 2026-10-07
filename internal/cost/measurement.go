package cost

import (
	"fmt"
	"math"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

type BillingKind string

const (
	BillingUnknown      BillingKind = "unknown"
	BillingAPI          BillingKind = "api"
	BillingSubscription BillingKind = "subscription"
)

type Quota struct {
	UsedPct       float64 `json:"used_pct"`
	WindowMinutes int     `json:"window_minutes"`
	Source        string  `json:"source"`
}

// Observation describes source coverage, not an invoice. Quotas never imply
// a billing kind, and token price conversion never proves money saved.
type Observation struct {
	Kind          BillingKind `json:"kind"`
	KindSource    string      `json:"kind_source"`
	UsageSource   string      `json:"usage_source"`
	UsageObserved bool        `json:"usage_observed"`
	UsageComplete bool        `json:"usage_complete"`
	PriceVersion  string      `json:"price_version"`
	Quota         *Quota      `json:"quota,omitempty"`
	Missing       []string    `json:"missing"`
}

type Measurement struct {
	Observation
	APIEquivalentMicro   *int64  `json:"api_equivalent_micro_krw"`
	ObservedTokens       *int64  `json:"observed_tokens"`
	PriceCoverage        float64 `json:"price_coverage"`
	ActualChargeMicro    *int64  `json:"actual_charge_micro_krw"`
	VerifiedSavingsMicro *int64  `json:"verified_savings_micro_krw"`
}

func ValidBillingKind(kind BillingKind) bool {
	return kind == "" || kind == BillingUnknown || kind == BillingAPI || kind == BillingSubscription
}

func ObserveSession(s *event.Session, priceVersion string) Observation {
	o := Observation{Kind: BillingUnknown, KindSource: "unconfirmed", UsageSource: "transcript", PriceVersion: priceVersion}
	if s == nil {
		o.Missing = []string{"사용량 출처가 없다"}
		return o
	}
	if s.Mode == "live" {
		o.UsageSource = "live_hooks_and_transcript"
	}
	o.UsageObserved = s.UsageParsed > 0
	for _, ev := range s.Events {
		if ev.Usage.Total() > 0 {
			o.UsageObserved = true
			break
		}
	}
	o.UsageComplete = s.UsageLines > 0 && s.UsageLines == s.UsageParsed && !s.FormatFailed
	for _, ev := range s.Events {
		if ev.Category == event.CatWatch && ev.Tool == "judge" && (ev.Usage.Total() == 0 || !ev.Priced) {
			o.UsageComplete = false
			o.Missing = append(o.Missing, "감시 판정 비용 미확인: 계측 누락을 무료로 계산할 수 없다")
			break
		}
	}
	if !o.UsageObserved {
		o.Missing = append(o.Missing, "사용량 계측이 없어 0으로 확정할 수 없다")
	}
	if !o.UsageComplete {
		o.Missing = append(o.Missing, "사용량 기록의 완전성을 확인하지 못했다")
	}
	if s.QuotaKnown && s.QuotaWindowMin > 0 {
		o.Quota = &Quota{UsedPct: s.QuotaUsedPct, WindowMinutes: s.QuotaWindowMin, Source: "agent_reported"}
	}
	return o
}

func Measure(o Observation, micro, tokens, unpricedTokens int64) Measurement {
	// Do not mutate caller-owned slices or quota objects.
	o.Missing = append([]string(nil), o.Missing...)
	if !ValidBillingKind(o.Kind) {
		o.Kind = BillingUnknown
		o.Missing = append(o.Missing, "과금 유형을 인식하지 못했다")
	}
	if o.Kind == "" {
		o.Kind = BillingUnknown
	}
	if o.KindSource == "" {
		o.KindSource = "unconfirmed"
	}
	if o.UsageSource == "" {
		o.UsageSource = "unspecified"
	}
	m := Measurement{Observation: o}
	if micro < 0 || tokens < 0 || unpricedTokens < 0 || unpricedTokens > tokens {
		m.Missing = append(m.Missing, "사용량 또는 비용 합계가 유효하지 않다")
		return m
	}
	if o.Quota != nil {
		q := *o.Quota
		m.Quota = &q
		if q.WindowMinutes <= 0 || q.UsedPct < 0 || q.UsedPct > 100 || math.IsNaN(q.UsedPct) || math.IsInf(q.UsedPct, 0) {
			m.Quota = nil
			m.Missing = append(m.Missing, "한도 계측값이 유효하지 않다")
		}
	}
	if tokens > 0 {
		m.UsageObserved = true
		m.PriceCoverage = float64(tokens-unpricedTokens) / float64(tokens)
	}
	if m.UsageObserved {
		m.ObservedTokens = &tokens
		if tokens == 0 {
			m.PriceCoverage = 1
		}
		if unpricedTokens == 0 {
			m.APIEquivalentMicro = &micro
		} else {
			m.Missing = append(m.Missing, fmt.Sprintf("단가 미인식 %d토큰: 전체 환산액 미확인", unpricedTokens))
		}
	} else if len(m.Missing) == 0 {
		m.Missing = append(m.Missing, "사용량 미계측: 금액과 토큰 합계 미확인")
	}
	if !m.UsageComplete && len(m.Missing) == 0 {
		m.Missing = append(m.Missing, "기록의 완전성 미확인; 계측된 부분만 표시한다")
	}
	return m
}
