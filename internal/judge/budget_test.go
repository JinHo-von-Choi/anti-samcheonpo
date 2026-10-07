package judge

import "testing"

func TestBudgetReservesBeforeDispatchAndCoalesces(t *testing.T) {
	var b Budget
	if err := b.Reserve("one", 60, 100, 2); err != nil {
		t.Fatal(err)
	}
	if err := b.Reserve("two", 20, 100, 2); err == nil {
		t.Fatal("concurrent request escaped reservation")
	}
	b.Settle(40, true)
	if err := b.Reserve("one", 20, 100, 2); err == nil {
		t.Fatal("duplicate input billed again")
	}
	if err := b.Reserve("two", 61, 100, 2); err == nil {
		t.Fatal("budget overrun dispatched")
	}
	if err := b.Reserve("two", 60, 100, 2); err != nil {
		t.Fatal(err)
	}
	b.Settle(30, true)
	if err := b.Reserve("three", 1, 100, 2); err == nil {
		t.Fatal("call limit ignored")
	}
	if b.Calls != 2 || b.SpentMicro != 70 || b.ReservedMicro != 0 {
		t.Fatalf("%+v", b)
	}
}

func TestUnknownChargeRetainsReservationAndOverrunStops(t *testing.T) {
	var b Budget
	if err := b.Reserve("failed", 50, 100, 3); err != nil {
		t.Fatal(err)
	}
	b.Settle(0, false)
	if !b.Unknown || b.ReservedMicro != 50 || b.SpentMicro != 0 {
		t.Fatalf("%+v", b)
	}
	if err := b.Reserve("retry", 1, 100, 3); err == nil {
		t.Fatal("unknown was treated as free")
	}
	b = Budget{}
	if err := b.Reserve("underestimated", 50, 100, 3); err != nil {
		t.Fatal(err)
	}
	b.Settle(70, true)
	if !b.Overrun || b.Unknown || b.SpentMicro != 70 {
		t.Fatalf("%+v", b)
	}
	if err := b.Reserve("more", 1, 100, 3); err == nil {
		t.Fatal("unsafe estimate allowed more calls")
	}
}

func TestReservationModelMatchesExistingProviderDefault(t *testing.T) {
	j, err := New(Config{Provider: "anthropic", BaseURL: "http://127.0.0.1:1", LocalOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if j.(*anthropicJudge).model != EffectiveModel("anthropic", "") {
		t.Fatal("reservation model differs from actual client")
	}
	if EffectiveModel("openai", "") != "" || EffectiveModel("anthropic", "custom") != "custom" {
		t.Fatal("configured model changed")
	}
}
