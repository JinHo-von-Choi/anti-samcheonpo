package verification

import (
	"testing"
	"time"
)

func passingEvidence(now time.Time) Evidence {
	return Evidence{Version: Version, ID: "e", Key: Key{TaskID: "t", Revision: 1, CheckID: "c", CommandHash: "cmd", InputHash: "input", EnvironmentHash: "env", RunnerVersion: "runner/1"}, SessionID: "s", SourceEventID: "event", ObservedAt: now, ExpiresAt: now.Add(time.Hour), Pass: true, Complete: true, ResultHash: "result"}
}

func TestEvidenceInvalidationMatrix(t *testing.T) {
	now := time.Now()
	base := passingEvidence(now)
	if ok, why := Reusable(base, base.Key, now); !ok {
		t.Fatal(why)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Evidence, *Key)
	}{
		{"input", func(e *Evidence, k *Key) { k.InputHash = "changed" }},
		{"environment", func(e *Evidence, k *Key) { k.EnvironmentHash = "changed" }},
		{"command", func(e *Evidence, k *Key) { k.CommandHash = "changed" }},
		{"revision", func(e *Evidence, k *Key) { k.Revision++ }},
		{"unknown", func(e *Evidence, k *Key) { k.InputHash = "" }},
		{"task", func(e *Evidence, k *Key) { k.TaskID = "other" }},
		{"flaky", func(e *Evidence, k *Key) { e.Flaky = true }},
		{"manual", func(e *Evidence, k *Key) { e.Manual = true }},
		{"partial", func(e *Evidence, k *Key) { e.Complete = false }},
		{"side-effect", func(e *Evidence, k *Key) { e.SideEffect = true }},
		{"timeout", func(e *Evidence, k *Key) { e.TimedOut = true }},
		{"failed", func(e *Evidence, k *Key) { e.Pass = false; e.ExitCode = 1 }},
		{"expired", func(e *Evidence, k *Key) { e.ExpiresAt = now }},
		{"future", func(e *Evidence, k *Key) { e.Version = "future" }},
		{"provenance", func(e *Evidence, k *Key) { e.SourceEventID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, k := base, base.Key
			tc.mutate(&e, &k)
			if ok, why := Reusable(e, k, now); ok || why == "" {
				t.Fatal("invalid evidence reused")
			}
		})
	}
	if ok, _ := Reusable(base, base.Key, now.Add(-time.Second)); ok {
		t.Fatal("future evidence reused")
	}
}
