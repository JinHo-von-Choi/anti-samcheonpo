package assessment

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"testing"
	"time"
)

func TestFindingWithoutAssignedCostIsNotClean(t *testing.T) {
	s := Build([]*event.Event{{Usage: event.Usage{In: 100}, Priced: true, CostMicroKRW: 100}}, []detect.Signal{{Rule: "s2.behavior_loop", Seq: 1, Estimate: true}}, config.Default(), 0, 0)
	if s.State != "candidate" || s.CostAttribution != "unattributed_findings" || s.Completion != "unknown" || s.WasteRatio != "0%" {
		t.Fatalf("%+v", s)
	}
	s = Build([]*event.Event{{Usage: event.Usage{In: 99999}, Priced: true, CostMicroKRW: 99999}, {Usage: event.Usage{In: 1}, Priced: true, CostMicroKRW: 1, Bucket: "waste"}}, nil, config.Default(), 0, 0)
	if s.WasteRatio != "<0.1%" || s.Confirmed != 1 {
		t.Fatal(s)
	}
}
func TestCoverageCountsShellResultsAndNeverInfersCollection(t *testing.T) {
	exit := -1
	s := Build([]*event.Event{{Kind: event.KindTool, Tool: event.ToolRead}, {Kind: event.KindTool, Tool: event.ToolShell, ExitCode: &exit}, {Kind: event.KindTool, Tool: event.ToolShell, Background: true}}, nil, config.Default(), 0, 0)
	if s.Coverage.ShellCalls != 1 || s.Coverage.CompletedShellResults != 0 || s.Coverage.TotalCollection != "unknown" || s.State != "insufficient_evidence" {
		t.Fatal(s)
	}
}
func linkedEvent() *event.Event {
	exit := 1
	return &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify, ExitCode: &exit, BehaviorTarget: "hash", BehaviorResult: "failure", Usage: event.Usage{In: 10}, Reliability: &event.Reliability{TaskID: "task", Revision: 1, EnvironmentHash: "env", InputHash: "input", Complete: true}}
}
func TestLinkedTaskRequiresExplicitLinksAndDeduplicatesMergedChildren(t *testing.T) {
	nodes := []TaskNode{{Link: intent.SessionLink{TaskID: "task", Agent: "claude", SessionID: "a"}, Events: []*event.Event{linkedEvent(), linkedEvent()}}, {Link: intent.SessionLink{TaskID: "task", Agent: "codex", SessionID: "b", ParentAgent: "claude", ParentSessionID: "a"}, Events: []*event.Event{linkedEvent()}}}
	s, err := LinkedTask(nodes)
	if err != nil || len(s.Cycles) != 1 || s.UsageTokens != 30 {
		t.Fatalf("%+v %v", s, err)
	}
	nodes[0].Link.IncludesChildren = true
	s, err = LinkedTask(nodes)
	if err != nil || s.Excluded != 1 || s.UsageTokens != 20 || len(s.Cycles) != 0 {
		t.Fatalf("%+v %v", s, err)
	}
	nodes[0].Link.ParentAgent = "codex"
	nodes[0].Link.ParentSessionID = "b"
	if _, err = LinkedTask(nodes); err == nil {
		t.Fatal("cyclic lineage accepted")
	}
}
func TestLinkedTaskDifferentRevisionIsNotCrossSessionLoop(t *testing.T) {
	nodes := []TaskNode{{Link: intent.SessionLink{TaskID: "task", Agent: "claude", SessionID: "a"}, Events: []*event.Event{linkedEvent(), linkedEvent()}}, {Link: intent.SessionLink{TaskID: "task", Agent: "codex", SessionID: "b"}, Events: []*event.Event{linkedEvent()}}}
	nodes[1].Events[0].Reliability.Revision = 2
	s, err := LinkedTask(nodes)
	if err != nil || len(s.Cycles) != 0 {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestHistoricalDeliveryDoesNotUseCurrentDefaults(t *testing.T) {
	vs := []detect.Signal{{ID: "a", Seq: 1, Rule: "s2.behavior_loop", Primary: true, Level: detect.L1, Estimate: true}, {ID: "b", Seq: 2, Rule: "s5.partial_done", Primary: true, Level: detect.L1, Estimate: true}}
	s := BuildRecorded(nil, vs, map[string]string{"a": "agent"})
	if s.Shadow != 0 || s.RolloutBasis != "recorded" || s.Reasons["rollout_unrecorded"] != 1 {
		t.Fatal(s)
	}
	s = BuildRecorded(nil, vs, map[string]string{"a": "shadow", "b": "agent"})
	if s.Shadow != 1 {
		t.Fatal(s)
	}
}

func TestLinkedTaskCannotMergeChangedInputsOrMissingContext(t *testing.T) {
	nodes := []TaskNode{{Link: intent.SessionLink{TaskID: "task", Agent: "claude", SessionID: "a"}, Events: []*event.Event{linkedEvent(), linkedEvent()}}, {Link: intent.SessionLink{TaskID: "task", Agent: "codex", SessionID: "b"}, Events: []*event.Event{linkedEvent()}}}
	nodes[1].Events[0].Reliability.InputHash = "changed"
	s, err := LinkedTask(nodes)
	if err != nil || len(s.Cycles) != 0 {
		t.Fatalf("%+v %v", s, err)
	}
	nodes[0].Events[0].Reliability.EnvironmentHash = ""
	nodes[0].Events[1].Reliability.EnvironmentHash = ""
	nodes[1].Events[0].Reliability.EnvironmentHash = ""
	s, err = LinkedTask(nodes)
	if err != nil || s.State != "insufficient_evidence" || s.Unknown != 3 {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestLinkedPassingResultBreaksPreviousCrossSessionFailure(t *testing.T) {
	nodes := []TaskNode{{Link: intent.SessionLink{TaskID: "task", Agent: "claude", SessionID: "a"}, Events: []*event.Event{linkedEvent(), linkedEvent()}}, {Link: intent.SessionLink{TaskID: "task", Agent: "codex", SessionID: "b"}, Events: []*event.Event{linkedEvent()}}}
	for i, e := range nodes[0].Events {
		e.TS = time.Unix(int64(i), 0)
	}
	nodes[1].Events[0].TS = time.Unix(3, 0)
	pass := linkedEvent()
	zero := 0
	pass.ExitCode = &zero
	pass.BehaviorResult = "passed"
	pass.TS = time.Unix(2, 0)
	nodes[1].Events = append([]*event.Event{pass}, nodes[1].Events...)
	s, err := LinkedTask(nodes)
	if err != nil || len(s.Cycles) != 0 {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestHandoffReturnUsesExplicitLineageActualEvidenceAndNewResultProtection(t *testing.T) {
	nodes := []TaskNode{
		{Link: intent.SessionLink{TaskID: "task", Agent: "claude", SessionID: "a"}, Events: []*event.Event{linkedEvent()}},
		{Link: intent.SessionLink{TaskID: "task", Agent: "codex", SessionID: "b", ParentAgent: "claude", ParentSessionID: "a"}, Events: []*event.Event{linkedEvent()}},
		{Link: intent.SessionLink{TaskID: "task", Agent: "claude", SessionID: "a-return", ParentAgent: "codex", ParentSessionID: "b"}, Events: []*event.Event{linkedEvent()}},
	}
	for i := range nodes {
		nodes[i].Events[0].Seq = int64(10 + i)
		nodes[i].Events[0].TS = time.Unix(int64(100+i), 0)
	}
	result, err := LinkedTask(nodes)
	if err != nil || len(result.Cycles) != 1 || len(result.Cycles[0].HandoffReturn) != 3 {
		t.Fatalf("%+v %v", result, err)
	}
	for i, ref := range result.Cycles[0].HandoffReturn {
		if ref.Seq != int64(10+i) {
			t.Fatal("invented handoff evidence")
		}
	}
	nodes[2].Events[0].BehaviorResult = "new evidence"
	result, err = LinkedTask(nodes)
	if err != nil || len(result.Cycles) != 0 {
		t.Fatal("new result counted as cyclic handoff")
	}
	nodes[2].Events[0].BehaviorResult = "failure"
	nodes[2].Link.ParentAgent = ""
	nodes[2].Link.ParentSessionID = ""
	result, err = LinkedTask(nodes)
	if err != nil || len(result.Cycles) != 1 || len(result.Cycles[0].HandoffReturn) != 0 {
		t.Fatal("inferred handoff without explicit parent")
	}
}
