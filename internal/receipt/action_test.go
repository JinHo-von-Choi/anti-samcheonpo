package receipt

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
)

func TestReceiptMissingUsageDoesNotLookFreeOrHealthy(t *testing.T) {
	r := Build(Input{Totals: analyze.Totals{}})
	if r.Unit != "unknown" || r.Billing.APIEquivalentMicro != nil || r.Billing.ObservedTokens != nil {
		t.Fatalf("%+v", r)
	}
	for _, out := range []string{r.Text(), r.Markdown()} {
		if !strings.Contains(out, "미확인") || strings.Contains(out, "0원") || strings.Contains(out, "0토큰") || strings.Contains(out, "100%") {
			t.Fatal(out)
		}
	}
	var decoded Receipt
	if err := json.Unmarshal([]byte(r.JSON()), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Billing.ActualChargeMicro != nil || decoded.Billing.APIEquivalentMicro != nil || decoded.Unit != "unknown" {
		t.Fatal("JSON changed unknown to zero")
	}
}

func TestSubscriptionReceiptDistinguishesObservedEquivalentAndQuota(t *testing.T) {
	r := Build(Input{Totals: totals(), Billing: cost.Observation{Kind: cost.BillingSubscription, KindSource: "user_declared", UsageObserved: true, UsageComplete: true, Quota: &cost.Quota{UsedPct: 0, WindowMinutes: 300, Source: "agent_reported"}}})
	if r.Billing.ActualChargeMicro != nil || r.Billing.VerifiedSavingsMicro != nil || r.Billing.APIEquivalentMicro == nil {
		t.Fatal("cash claim")
	}
	for _, out := range []string{r.Text(), r.Markdown()} {
		if !strings.Contains(out, "총 API 환산액") || !strings.Contains(out, "5시간 창 0%") || strings.Contains(out, "총 지출") {
			t.Fatal(out)
		}
	}
}

func TestActionSeparatesEstimatesAndUnconfirmedRecoveryAndShareRedacts(t *testing.T) {
	now := time.Now()
	a := recovery.Attempt{Prescription: recovery.For(recovery.Environment), Stage: recovery.Emitted, EmittedAt: &now}
	in := Input{Totals: totals(), Criteria: [2]int{1, 2}, Verdicts: []detect.Signal{{Primary: true, Estimate: true, Confidence: .8, Detector: "S3", Rule: "s3.out_of_scope", Level: detect.L2, Evidence: []int64{7}, Facts: map[string]any{"path": "secret/customer.go"}}}, Recoveries: []recovery.Attempt{a}}
	r := Build(in)
	if len(r.Action.Estimates) != 1 || r.Action.Estimates[0].Evidence[0] != 7 || !strings.Contains(r.Action.NextAction, "권한") {
		t.Fatalf("%+v", r.Action)
	}
	if !strings.Contains(r.Action.Text(), "전달이 확인되지") || !strings.Contains(r.Action.Text(), "관찰 근거가 부족") {
		t.Fatal(r.Action.Text())
	}
	if strings.Contains(r.Share().JSON(), "secret") || strings.Contains(r.Share().Text(), "secret") {
		t.Fatal("share leaked action evidence")
	}
	a.DeliveredAt = &now
	a.Stage = recovery.EffectObserved
	a.Observation = "no_recurrence_observed"
	in.Recoveries = []recovery.Attempt{a}
	if action := SummarizeAction(in); !strings.Contains(action.Text(), "입증되지 않는다") {
		t.Fatal(action.Text())
	}
}

func TestPartialPriceCoverageUsesTokensNotCompleteMoney(t *testing.T) {
	total := totals()
	total.UnpricedTokens = 1
	r := Build(Input{Totals: total})
	if r.Unit != "tokens" || r.Billing.APIEquivalentMicro != nil || !strings.Contains(r.Text(), "전체 환산액 미확인") {
		t.Fatalf("%+v", r)
	}
}

func TestChangedIntentDoesNotRecommendOldRecovery(t *testing.T) {
	a := recovery.Attempt{Observation: "intent_revision_changed", Prescription: recovery.Prescription{Action: "outdated action"}}
	s := SummarizeAction(Input{Recoveries: []recovery.Attempt{a}})
	if strings.Contains(s.Text(), "outdated action") {
		t.Fatal("stale recovery became next action")
	}
}
