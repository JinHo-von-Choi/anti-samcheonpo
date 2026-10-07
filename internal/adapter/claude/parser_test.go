package claude

import (
	"path/filepath"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func td(p string) string { return filepath.Join("..", "..", "..", "testdata", "claude", p) }

func TestBasicTranscript(t *testing.T) {
	s, err := ParseFile(td("basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "s1" || s.ProjectPath != "/w/proj" || s.Model != "claude-sonnet-5-5" {
		t.Fatalf("session meta %+v", s)
	}
	if s.FirstPrompt != "src/a.py의 버그를 고쳐 줘" {
		t.Errorf("first prompt %q", s.FirstPrompt)
	}
	var tools []*event.Event
	var compacts, msgs int
	var total event.Usage
	for _, ev := range s.Events {
		total.Add(ev.Usage)
		switch ev.Kind {
		case event.KindTool:
			tools = append(tools, ev)
		case event.KindCompact:
			compacts++
		case event.KindMessage:
			msgs++
		}
	}
	// message msg_1 spans three lines with the same usage; it must count once
	if total.Out != 41+30+20+9 || total.In != 10+3+2+1 || total.CacheRead != 1000+1300+1400+1500 || total.CacheWrite != 250 {
		t.Errorf("usage totals %+v", total)
	}
	if total.CacheWrite1h != 200 {
		t.Errorf("1h cache writes %d", total.CacheWrite1h)
	}
	if len(tools) != 4 || compacts != 1 {
		t.Fatalf("tools=%d compacts=%d", len(tools), compacts)
	}
	if s.ToolUses != 4 || s.ToolUsesPaired != 4 {
		t.Errorf("pairing %d/%d", s.ToolUsesPaired, s.ToolUses)
	}
	read, bash, edit, write := tools[0], tools[1], tools[2], tools[3]
	if read.Tool != event.ToolRead || read.Paths[0] != "src/a.py" {
		t.Errorf("read %+v", read)
	}
	// msg_1 had two tool calls: tokens split evenly, remainder on the first
	if read.Usage.Out != 21 || bash.Usage.Out != 20 || read.Usage.CacheRead+bash.Usage.CacheRead != 1000 {
		t.Errorf("split read=%+v bash=%+v", read.Usage, bash.Usage)
	}
	if bash.Tool != event.ToolShell || bash.CmdNorm != "pytest -q" || bash.Dir != "/w/proj" {
		t.Errorf("bash %+v", bash)
	}
	if bash.ExitCode == nil || *bash.ExitCode != 1 || len(bash.FailedTests) != 1 || bash.FailedTests[0] != "tests/test_a.py::test_f" {
		t.Errorf("bash result exit=%v failed=%v", bash.ExitCode, bash.FailedTests)
	}
	// edit result hash equals the hash of the edited content
	if edit.WriteHashes["src/a.py"] != hashStr("def f():\n    return 2\n") {
		t.Errorf("edit hash %v", edit.WriteHashes)
	}
	if write.Tool != event.ToolWrite || len(write.Created) != 1 || write.Created[0] != "src/b.py" {
		t.Errorf("write %+v", write)
	}
	if s.ReportedCostUSD != 0.01 {
		t.Errorf("reported cost %v", s.ReportedCostUSD)
	}
	last := s.Events[len(s.Events)-1]
	if last.Kind != event.KindMessage || last.Text != "고쳤습니다. 완료했습니다." {
		t.Errorf("last event %+v", last)
	}
	for i, ev := range s.Events {
		if ev.Seq != int64(i) || ev.SessionID != "s1" || ev.SourceRef == "" && ev.Kind != event.KindMessage {
			t.Errorf("event %d seq=%d sid=%s ref=%q", i, ev.Seq, ev.SessionID, ev.SourceRef)
		}
	}
}

func TestSubagentsMerged(t *testing.T) {
	s, err := ParseFile(td("sub.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var sub *event.Event
	for _, ev := range s.Events {
		if ev.RawTool == "Grep" {
			sub = ev
		}
	}
	if sub == nil || !sub.Parent || sub.Usage.In != 100 || sub.Usage.Model != "claude-haiku-4-5" {
		t.Fatalf("subagent event %+v", sub)
	}
	for i := 1; i < len(s.Events); i++ {
		if s.Events[i].TS.Before(s.Events[i-1].TS) || s.Events[i].Seq != int64(i) {
			t.Fatal("merged events must be time ordered with renumbered seq")
		}
	}
}

func TestUsageTrackerPartialLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	line := `{"type":"assistant","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"x1","name":"Bash","input":{}}],"usage":{"input_tokens":5,"output_tokens":7,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`
	if err := writeFile(p, line[:40]); err != nil {
		t.Fatal(err)
	}
	u := NewUsageTracker(p)
	ups, _ := u.Poll()
	if len(ups) != 0 || u.Offset != 0 {
		t.Fatalf("partial line must not be consumed: %v off=%d", ups, u.Offset)
	}
	if err := writeFile(p, line+"\n"); err != nil {
		t.Fatal(err)
	}
	ups, _ = u.Poll()
	if len(ups) != 1 || ups[0].ToolUseID != "x1" || ups[0].Delta.Out != 7 {
		t.Fatalf("updates %+v", ups)
	}
	ups, _ = u.Poll()
	if len(ups) != 0 {
		t.Fatal("no new data, no updates")
	}
}

func TestUsageTrackerMovesEarlyMessageUsage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	text := `{"type":"assistant","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"text","text":"보겠습니다"}],"usage":{"input_tokens":4,"output_tokens":10,"cache_read_input_tokens":100,"cache_creation_input_tokens":0}}}` + "\n"
	tool := `{"type":"assistant","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"x1","name":"Bash","input":{}}],"usage":{"input_tokens":4,"output_tokens":30,"cache_read_input_tokens":100,"cache_creation_input_tokens":0}}}` + "\n"
	_ = writeFile(p, text)
	u := NewUsageTracker(p)
	first, _ := u.Poll()
	if len(first) != 1 || first[0].ToolUseID != "" || first[0].Delta.Out != 10 {
		t.Fatalf("text-only usage goes to the message: %+v", first)
	}
	_ = writeFile(p, text+tool)
	ups, _ := u.Poll()
	var msg, call event.Usage
	for _, x := range ups {
		if x.ToolUseID == "" {
			msg.Add(x.Delta)
		} else {
			call.Add(x.Delta)
		}
	}
	if msg.Out != -10 || call.Out != 30 || call.In != 4 || call.CacheRead != 100 {
		t.Fatalf("the message gives back its usage and the call gets all of it: msg=%+v call=%+v", msg, call)
	}
	for _, x := range ups {
		if x.ToolUseID != "" && (x.Delta.Out < 0 || x.Delta.In < 0) {
			t.Fatalf("tool deltas must not be negative: %+v", x)
		}
	}
}
