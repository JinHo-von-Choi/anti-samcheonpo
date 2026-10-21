package adapter

import (
	"testing"
	"time"
)

func TestCursorExactBuildAndObservation(t *testing.T) {
	for _, version := range []string{"2026.10.19-6bbf2c4", "2026.10.19-other", "2026.10.20-6bbf2c4", ""} {
		caps, tested := Resolve("cursor", version)
		if tested != (version == "2026.10.19-6bbf2c4") {
			t.Fatalf("tested %s=%v", version, tested)
		}
		if caps.BlockPre || caps.InjectPost || caps.BlockStop || !caps.FailClosed {
			t.Fatalf("unverified capability %+v", caps)
		}
	}
	if Observe("cursor").SessionEndBudget != 5*time.Second {
		t.Fatal("lost host session end budget")
	}
}
