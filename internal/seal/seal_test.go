package seal

import (
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func evs() []*event.Event {
	one := 1
	return []*event.Event{
		{Seq: 0, Kind: event.KindTool, Tool: "shell", CmdFP: "c", Paths: []string{"/home/me/secret/app.py"}, ExitCode: &one, FailedTests: []string{"tests/test_x.py::t"},
			Usage: event.Usage{In: 1, Out: 2, Model: "m"}, CostMicroKRW: 10, Priced: true, Bucket: "waste", Symptom: "S1"},
		{Seq: 1, Kind: event.KindMessage, Bucket: "other", WriteHashes: map[string]string{"b.go": "h", "a.go": "g"}},
	}
}

func TestChainDetectsTampering(t *testing.T) {
	rows := Build(evs(), []detect.Signal{{Seq: 0, Detector: "S1", Rule: "s1.identical_rerun", Confidence: 0.95, Evidence: []int64{0}}})
	if CheckChain(rows) != -1 {
		t.Fatal("fresh chain must be intact")
	}
	if Head(rows) != rows[len(rows)-1].Chain {
		t.Fatal("head is the last link")
	}
	tampered := append([]Row(nil), rows...)
	tampered[0].Data = strings.Replace(tampered[0].Data, `"cost_micro_krw":10`, `"cost_micro_krw":11`, 1)
	if CheckChain(tampered) != 0 {
		t.Fatal("a modified row must break the chain at that row")
	}
}

func TestCanonicalIsDeterministicAndRedacted(t *testing.T) {
	a := CanonicalEvent(evs()[1])
	b := CanonicalEvent(evs()[1])
	if a != b {
		t.Fatal("canonical form must be stable (map order)")
	}
	c := CanonicalEvent(evs()[0])
	if strings.Contains(c, "/home/me") || strings.Contains(c, "test_x") || strings.Contains(c, "secret") {
		t.Errorf("paths and test names must be hashed: %s", c)
	}
}

// TestSpecExample pins the worked example in docs/spec/evidence-ledger-v1.md §8.
func TestSpecExample(t *testing.T) {
	one := 1
	ev := &event.Event{Seq: 0, TS: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), Kind: event.KindTool, Tool: "shell", CmdFP: "4f2a", Paths: []string{"src/a.py"},
		WSBefore: "ws1", WSAfter: "ws1", ExitCode: &one, FailedTests: []string{"tests/test_a.py::test_f"}, ResultFP: "r1",
		Usage: event.Usage{In: 10, Out: 20, CacheRead: 1000, Model: "claude-sonnet-5-5"}, CostMicroKRW: 732040, Priced: true, Category: "verify", Bucket: "waste", Symptom: "S1"}
	v := detect.Signal{Seq: 0, Detector: "S1", Rule: "s1.identical_rerun", Confidence: 0.95, Level: 1, Evidence: []int64{0}, Primary: true}
	rows := Build([]*event.Event{ev}, []detect.Signal{v})
	if PathID("src/a.py") != "9d3578cb9bdba9e920085e06f11fca16" {
		t.Errorf("PathID %s", PathID("src/a.py"))
	}
	if rows[0].Chain != "892dacd795d4125b6da515995638d83b8fa642fd206438d121d44770aeae80d6" ||
		Head(rows) != "33ac78bad2ea4e7765ca99e29234259973936f614272c34f8b055f014a270068" {
		t.Errorf("chain differs from the spec example: %s %s", rows[0].Chain, Head(rows))
	}
}
