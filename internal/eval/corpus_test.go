package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func corpusFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	raw := []byte("goal: fixture\ndone: [{id: test, check: pytest}]\ntemporal_requirement: {kind: completion}\n")
	c, errs := contract.Parse(raw)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if err := os.WriteFile(filepath.Join(root, "contract.yml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := CorpusManifest{Version: CorpusVersion, Cohort: "local-fixture"}
	labels := CorpusLabels{Version: CorpusVersion, Labeler: "local-regression-fixture"}
	for _, name := range []string{"wait-positive", "wait-pending", "wait-linked", "wait-unknown", "stall-positive", "stall-improved"} {
		var events []*event.Event
		for n := int64(1); n <= 5; n++ {
			exit := 0
			r := &event.Reliability{TaskID: "task", Revision: 1, AuthorityHash: contract.AuthorityDigest(c), Complete: true, CriteriaMet: true, Observation: "none", Phase: "result"}
			ev := &event.Event{SessionID: name, Seq: n, Kind: event.KindTool, Tool: event.ToolRead, CallID: fmt.Sprint(n), TS: time.Unix(100+n, 0), ExitCode: &exit, Reliability: r}
			switch name {
			case "wait-positive", "wait-pending", "wait-linked", "wait-unknown":
				ev.Tool = event.ToolShell
				r.Wait = "explicit"
				r.WaitKind = "sleep"
				if name == "wait-pending" {
					r.Observation = "pending"
				}
				if name == "wait-linked" {
					r.Wait = "linked"
				}
				if name == "wait-unknown" {
					r.TaskID = ""
					r.CriteriaMet = false
					r.Observation = "unknown"
				}
			case "stall-positive", "stall-improved":
				if n%2 == 1 {
					exit = 1
					ev.Tool = "checkpoint_check"
					ev.Category = event.CatVerify
					ev.FailedTests = []string{"A", "B"}
					r.CheckID = "test"
					r.InputHash = fmt.Sprint(n)
					r.EnvironmentHash = "same-env"
					r.CommandHash = "same-check"
					r.FailureComparable = true
					if name == "stall-improved" && n >= 3 {
						ev.FailedTests = []string{"A"}
					}
				}
			}
			events = append(events, ev)
		}
		b, err := json.MarshalIndent(events, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		path := name + ".json"
		if err = os.WriteFile(filepath.Join(root, path), b, 0600); err != nil {
			t.Fatal(err)
		}
		manifest.Runs = append(manifest.Runs, CorpusRun{ID: name, Benchmark: "local", TaskRawID: name, Split: "eval", Path: path, Format: "normalized", SHA256: corpusDigest(b), ContractPath: "contract.yml", ContractSHA256: corpusDigest(raw), Provenance: "regression scenario fixed before scoring"})
		positive := name == "wait-positive" || name == "stall-positive"
		for _, rule := range []string{"s1.explicit_waiting", "s8.progress_stall"} {
			label := CorpusLabel{RunID: name, Rule: rule, Positive: positive && ((name == "wait-positive" && rule == "s1.explicit_waiting") || (name == "stall-positive" && rule == "s8.progress_stall"))}
			if rule == "s1.explicit_waiting" {
				raw := name == "wait-positive" || name == "wait-pending" || name == "wait-linked" || name == "wait-unknown"
				label.RawWait = &raw
			}
			labels.Labels = append(labels.Labels, label)
		}
	}
	write := func(name string, v any) string {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, name)
		if err = os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	return write("manifest.json", manifest), write("labels.json", labels)
}
func TestCorpusIsolatedReplayAndLabelIndependence(t *testing.T) {
	manifest, labels := corpusFixture(t)
	sentinel := filepath.Join(t.TempDir(), "ledger.db")
	t.Setenv("SAMCHEONPO_HOME", filepath.Dir(sentinel))
	if err := os.WriteFile(sentinel, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	rep, err := EvaluateCorpus(manifest, labels, "eval")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Excluded != 0 || len(rep.Runs) != 6 || rep.EffectivenessValidated {
		t.Fatalf("%+v", rep)
	}
	for _, score := range rep.Rules {
		if score.Overall.TP != 1 || score.Overall.FP != 0 || score.Overall.FN != 0 || score.Delivered != 0 {
			t.Fatalf("unexpected score %+v", score)
		}
	}
	before, err := os.ReadFile(sentinel)
	if err != nil || string(before) != "unchanged" {
		t.Fatal("user ledger touched", err)
	}
	b, _ := os.ReadFile(labels)
	var annotations CorpusLabels
	if err = corpusJSON(b, &annotations); err != nil {
		t.Fatal(err)
	}
	for i := range annotations.Labels {
		annotations.Labels[i].Positive = !annotations.Labels[i].Positive
	}
	b, _ = json.Marshal(annotations)
	if err = os.WriteFile(labels, b, 0600); err != nil {
		t.Fatal(err)
	}
	again, err := EvaluateCorpus(manifest, labels, "eval")
	if err != nil {
		t.Fatal(err)
	}
	for i, run := range rep.Runs {
		x, _ := json.Marshal(run)
		y, _ := json.Marshal(again.Runs[i])
		if string(x) != string(y) {
			t.Fatal("labels influenced predictions")
		}
	}
	unlabeled, err := EvaluateCorpus(manifest, "", "eval")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range unlabeled.Rules {
		if s.Labeled != 0 || s.Overall.Precision != nil {
			t.Fatal("missing labels scored")
		}
	}
}
func TestCorpusRejectsLeakageAndReportsHashFailures(t *testing.T) {
	path, labels := corpusFixture(t)
	b, _ := os.ReadFile(path)
	var m CorpusManifest
	if err := corpusJSON(b, &m); err != nil {
		t.Fatal(err)
	}
	m.Runs[1].TaskRawID = m.Runs[0].TaskRawID
	m.Runs[1].Split = "train"
	b, _ = json.Marshal(m)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := EvaluateCorpus(path, labels, "eval"); err == nil {
		t.Fatal("group leaked")
	}
	m.Runs[1].Split = "eval"
	m.Runs[0].SHA256 = fmt.Sprintf("%064x", 0)
	b, _ = json.Marshal(m)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	rep, err := EvaluateCorpus(path, labels, "eval")
	if err != nil || rep.Excluded != 1 || rep.Runs[0].Excluded == "" {
		t.Fatal(rep.Excluded, err)
	}
}

func TestCorpusNativeClaudeNormalization(t *testing.T) {
	root := t.TempDir()
	id := "native"
	var source []byte
	for n := 0; n < 3; n++ {
		source = append(source, []byte(fmt.Sprintf(`{"type":"assistant","sessionId":"native","timestamp":"2026-10-10T00:00:0%dZ","message":{"role":"assistant","content":[{"type":"tool_use","id":"call%d","name":"Bash","input":{"command":"sleep 1"}}]}}`+"\n", n, n))...)
		source = append(source, []byte(fmt.Sprintf(`{"type":"user","sessionId":"native","timestamp":"2026-10-10T00:00:0%dZ","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call%d","content":""}]}}`+"\n", n, n))...)
	}
	if err := os.WriteFile(filepath.Join(root, "native.jsonl"), source, 0600); err != nil {
		t.Fatal(err)
	}
	m := CorpusManifest{Version: CorpusVersion, Cohort: "local-fixture", Runs: []CorpusRun{{ID: id, Benchmark: "local", TaskRawID: id, Split: "eval", Format: "claude", Path: "native.jsonl", SHA256: corpusDigest(source)}}}
	b, _ := json.Marshal(m)
	path := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	rep, err := EvaluateCorpus(path, "", "eval")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Excluded != 0 || rep.Runs[0].RawWaits != 3 || rep.Runs[0].Rules["s1.explicit_waiting"] {
		t.Fatalf("native raw facts or policy context: %+v", rep.Runs)
	}
}

func TestCorpusDeliveryProfileIsSeparateFromDetection(t *testing.T) {
	manifest, labels := corpusFixture(t)
	baseline, err := EvaluateCorpus(manifest, labels, "eval")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(manifest)
	var m CorpusManifest
	if err = corpusJSON(b, &m); err != nil {
		t.Fatal(err)
	}
	m.EvaluationRollout = &CorpusRollout{Experiment: true, Mode: "recommend", ShadowRules: []string{}}
	b, _ = json.Marshal(m)
	if err = os.WriteFile(manifest, b, 0600); err != nil {
		t.Fatal(err)
	}
	rep, err := EvaluateCorpus(manifest, labels, "eval")
	if err != nil {
		t.Fatal(err)
	}
	for i, rr := range rep.Runs {
		for _, rule := range []string{"s1.explicit_waiting", "s8.progress_stall"} {
			if rr.Rules[rule] != baseline.Runs[i].Rules[rule] {
				t.Fatal("delivery profile changed detection")
			}
			if rr.Rules[rule] && rr.DeliveryArms[rule] == "" {
				t.Fatal("experiment arm missing")
			}
			if rr.Delivered[rule] && rr.DeliveryArms[rule] == "none" {
				t.Fatal("no-delivery arm notified")
			}
		}
	}
}
