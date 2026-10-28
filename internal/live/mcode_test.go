package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

const mcodeFixtures = "../../testdata/hooks/mcode/"

func mcodeTestSession(t *testing.T) (*Session, *Daemon) {
	t.Helper()
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	if err := os.WriteFile(filepath.Join(root, ".samcheonpo.yml"), []byte("notify: {desktop: false}\niron_laws: {enabled: false}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := ledger.Open(filepath.Join(home, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	prices, err := cost.Load("")
	if err != nil {
		t.Fatal(err)
	}
	s, err := newSession("mcode-test", "mcode", root, "", db, prices, adapter.Observe("mcode"))
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{sessions: map[string]*Session{s.ID: s}, db: db, prices: prices, versions: map[string]string{"mcode": "0.6.13"}}
	t.Cleanup(func() {
		if _, err := s.Finalize(); err != nil {
			t.Error(err)
		}
		db.Close()
	})
	return s, d
}

// mcodeFixture reads a captured payload and points it at the test session.
func mcodeFixture(t *testing.T, name string, s *Session) (string, json.RawMessage) {
	t.Helper()
	raw, err := os.ReadFile(mcodeFixtures + name)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if s != nil {
		m["session_id"] = s.ID
		m["cwd"] = s.Root
	}
	b, _ := json.Marshal(m)
	return m["hook_event_name"].(string), b
}

func mcodeSend(t *testing.T, s *Session, d *Daemon, file string) json.RawMessage {
	t.Helper()
	name, raw := mcodeFixture(t, file, s)
	out, _, err := d.handle(Request{Agent: "mcode", Event: name, Payload: raw})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mcodeTools(t *testing.T, s *Session, d *Daemon) []*event.Event {
	t.Helper()
	if _, err := s.Finalize(); err != nil {
		t.Fatal(err)
	}
	events, err := d.db.EventsOf(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	var tools []*event.Event
	for _, ev := range events {
		if ev.Kind == event.KindTool {
			tools = append(tools, ev)
		}
	}
	return tools
}

// TestMcodeCapturedFixtures reads every payload captured from mcode 0.6.13.
func TestMcodeCapturedFixtures(t *testing.T) {
	files, err := filepath.Glob(mcodeFixtures + "*.json")
	if err != nil || len(files) < 40 {
		t.Fatalf("fixtures %d %v", len(files), err)
	}
	for _, path := range files {
		base := filepath.Base(path)
		if base == "manifest.json" {
			continue
		}
		name, raw := mcodeFixture(t, base, nil)
		names, ins, err := translateIn("mcode", name, raw)
		if err != nil {
			t.Fatalf("%s: %v", base, err)
		}
		for i, n := range names {
			if reason := hookShape(n, ins[i]); reason != "" {
				t.Fatalf("%s: %s", base, reason)
			}
			if ins[i].Mcode == nil {
				t.Fatalf("%s: no mcode evidence", base)
			}
		}
	}
}

func TestMcodeTranslatesCapturedResults(t *testing.T) {
	for _, tc := range []struct {
		file, event, tool, state, task string
		exit                           *int
	}{
		{"shell-003-PostToolUse.json", "PostToolUse", "Bash", "exited", "", intPtr(0)},
		{"shell-005-PostToolUse.json", "PostToolUseFailure", "Bash", "failed", "", intPtr(7)},
		{"background-003-PostToolUse.json", "PostToolUseFailure", "Bash", "timeout", "", nil},
		{"background-009-PostToolUse.json", "PostToolUse", "Bash", "background", "bg_task-4", nil},
		{"background-015-PostToolUse.json", "BackgroundResult", "task_output", "failed", "bg_task-4", intPtr(3)},
		{"background-011-PostToolUse.json", "BackgroundResult", "task_output", "unknown", "bg_task-4", nil},
		{"shell-007-PostToolUse.json", "PostToolUse", "Write", "exited", "", nil},
		{"shell-009-PostToolUse.json", "PostToolUse", "Edit", "exited", "", nil},
	} {
		t.Run(tc.file, func(t *testing.T) {
			name, raw := mcodeFixture(t, tc.file, nil)
			names, ins, err := translateIn("mcode", name, raw)
			if err != nil || len(names) == 0 {
				t.Fatalf("names=%v err=%v", names, err)
			}
			e := ins[0].Mcode.Result
			if names[0] != tc.event || ins[0].ToolName != tc.tool || e.State != tc.state || e.TaskID != tc.task {
				t.Fatalf("got event=%s tool=%s state=%s task=%s", names[0], ins[0].ToolName, e.State, e.TaskID)
			}
			if (e.Exit == nil) != (tc.exit == nil) || tc.exit != nil && *e.Exit != *tc.exit {
				t.Fatalf("exit=%v want %v", e.Exit, tc.exit)
			}
		})
	}
	// write states its path as "path"; the parser reads file_path
	_, raw := mcodeFixture(t, "shell-006-PreToolUse.json", nil)
	_, ins, _ := translateIn("mcode", "PreToolUse", raw)
	var input map[string]string
	_ = json.Unmarshal(ins[0].ToolInput, &input)
	if input["file_path"] != "/w/proj/note.txt" {
		t.Fatalf("file_path not set: %v", input)
	}
	// a finished subagent call names the session it ran in
	_, raw = mcodeFixture(t, "subagent-003-SubagentStart.json", nil)
	var child struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(raw, &child)
	_, raw = mcodeFixture(t, "subagent-007-PostToolUse.json", nil)
	_, ins, _ = translateIn("mcode", "PostToolUse", raw)
	if ins[0].ToolName != "Agent" || child.SessionID == "" || ins[0].Mcode.Result.Child != child.SessionID {
		t.Fatalf("child=%q tool=%q", ins[0].Mcode.Result.Child, ins[0].ToolName)
	}
	// subagent lifecycle events carry no parent and are not turned into calls
	for _, f := range []string{"subagent-003-SubagentStart.json", "subagent-006-SubagentStop.json"} {
		name, raw := mcodeFixture(t, f, nil)
		if names, _, err := translateIn("mcode", name, raw); err != nil || len(names) != 0 {
			t.Fatalf("%s: %v %v", f, names, err)
		}
	}
	// the task start tools are not calls of their own
	name, raw := mcodeFixture(t, "background-010-PreToolUse.json", nil)
	if names, _, _ := translateIn("mcode", name, raw); len(names) != 0 {
		t.Fatalf("task_output pre-tool must be ignored: %v", names)
	}
}

func TestMcodeBackgroundNoticeIsNotARequest(t *testing.T) {
	_, raw := mcodeFixture(t, "background-016-UserPromptSubmit.json", nil)
	var m struct {
		Prompt string `json:"prompt"`
	}
	_ = json.Unmarshal(raw, &m)
	if !intent.HarnessNotice(m.Prompt) {
		t.Fatalf("background notice read as a user request: %q", m.Prompt[:40])
	}
	_, raw = mcodeFixture(t, "shell-001-UserPromptSubmit.json", nil)
	_ = json.Unmarshal(raw, &m)
	if intent.HarnessNotice(m.Prompt) {
		t.Fatal("a user prompt read as a notice")
	}
}

func TestMcodeOutputFollowsTheDeliveredShapes(t *testing.T) {
	deny := mcodeOut("PreToolUse", nil, map[string]any{"permissionDecision": "deny", "permissionDecisionReason": "같은 실패 반복"})
	if string(deny) != `{"decision":"block","reason":"같은 실패 반복"}` {
		t.Fatalf("deny=%s", deny)
	}
	if out := mcodeOut("PreToolUse", nil, map[string]any{"permissionDecision": "allow"}); out != nil {
		t.Fatalf("allow must print nothing: %s", out)
	}
	post := mcodeOut("PostToolUse", map[string]any{}, map[string]any{"additionalContext": "안내"})
	if !strings.Contains(string(post), `"additionalContext":"안내"`) || !strings.Contains(string(post), `"hookEventName":"PostToolUse"`) {
		t.Fatalf("post=%s", post)
	}
	// a turn end the dialect asks for reaches mcode as guidance
	end := mcodeOut("PostToolUse", map[string]any{"continue": false, "stopReason": "멈춤"}, nil)
	if !strings.Contains(string(end), `"additionalContext":"멈춤"`) {
		t.Fatalf("end=%s", end)
	}
	hold := mcodeOut("Stop", map[string]any{"decision": "block", "reason": "확인 필요"}, nil)
	if string(hold) != `{"decision":"block","reason":"확인 필요"}` {
		t.Fatalf("hold=%s", hold)
	}
}

func TestMcodeShellResultsKeepTheirStatedExitCode(t *testing.T) {
	s, d := mcodeTestSession(t)
	for _, f := range []string{"shell-002-PreToolUse.json", "shell-003-PostToolUse.json", "shell-004-PreToolUse.json", "shell-005-PostToolUse.json"} {
		mcodeSend(t, s, d, f)
	}
	tools := mcodeTools(t, s, d)
	if len(tools) != 2 {
		t.Fatalf("tools=%d", len(tools))
	}
	for i, want := range []int{0, 7} {
		if tools[i].ExitCode == nil || *tools[i].ExitCode != want {
			t.Fatalf("call %d exit=%v want %d", i, tools[i].ExitCode, want)
		}
	}
}

// TestMcodeBackgroundRunCompletesLater replays the captured background run:
// the start keeps no exit code, a running task_output changes nothing, the
// final task_output adds one completion event, and a repeat adds none.
func TestMcodeBackgroundRunCompletesLater(t *testing.T) {
	s, d := mcodeTestSession(t)
	for _, f := range []string{"background-008-PreToolUse.json", "background-009-PostToolUse.json", "background-011-PostToolUse.json"} {
		mcodeSend(t, s, d, f)
	}
	s.mu.Lock()
	run := s.background["bg_task-4"]
	s.mu.Unlock()
	if run == nil {
		t.Fatal("background start not tracked")
	}
	// the captured notice states an end before this test began, which is no
	// end of this run; a notice stating a later end is taken
	mcodeSend(t, s, d, "background-016-UserPromptSubmit.json")
	// a stated end on the launch's own millisecond leaves no run time; the
	// run lasts a moment in the test so the stated end lies after it
	time.Sleep(5 * time.Millisecond)
	stated := time.Now()
	notice := `<background-task-finished>
  <task task_id="bg_task-4" status="failed" description="x" ended_at_ms="` + strconv.FormatInt(stated.UnixMilli(), 10) + `"/>
</background-task-finished>`
	prompt, _ := json.Marshal(map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": s.ID, "cwd": s.Root, "prompt": notice})
	if _, _, err := d.handle(Request{Agent: "mcode", Event: "UserPromptSubmit", Payload: prompt}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	ended := s.background["bg_task-4"].endedAt
	s.mu.Unlock()
	if ended.IsZero() {
		t.Fatal("notice end time not recorded")
	}
	mcodeSend(t, s, d, "background-015-PostToolUse.json")
	mcodeSend(t, s, d, "background-015-PostToolUse.json")
	tools := mcodeTools(t, s, d)
	if len(tools) != 2 {
		t.Fatalf("want start and completion, got %d", len(tools))
	}
	start, done := tools[0], tools[1]
	if !start.Background || start.ExitCode != nil {
		t.Fatalf("start must stay a launch: bg=%v exit=%v", start.Background, start.ExitCode)
	}
	if done.Background || done.OriginSeq != start.Seq || done.ExitCode == nil || *done.ExitCode != 3 {
		t.Fatalf("completion bg=%v origin=%d exit=%v", done.Background, done.OriginSeq, done.ExitCode)
	}
	if done.WSBefore != start.WSBefore || done.CmdFP != start.CmdFP || done.Category != start.Category {
		t.Fatal("completion must keep the command and workspace it ran on")
	}
	if done.Execution == nil || done.Execution.ProcessID != "bg_task-4" || done.Execution.ExitSource != "background_result" {
		t.Fatalf("execution=%+v", done.Execution)
	}
	if done.DurationMS <= 0 || done.TS.After(ended) {
		t.Fatalf("duration %d must run to the stated end %v (ts %v)", done.DurationMS, ended, done.TS)
	}
}

func TestMcodeBackgroundWithoutResultIsRecordedAsAGap(t *testing.T) {
	s, d := mcodeTestSession(t)
	for _, f := range []string{"background-008-PreToolUse.json", "background-009-PostToolUse.json"} {
		mcodeSend(t, s, d, f)
	}
	s.closeBackground()
	tools := mcodeTools(t, s, d)
	if len(tools) != 1 {
		t.Fatalf("tools=%d", len(tools))
	}
	found := false
	for _, dec := range tools[0].Decisions {
		if dec.Rule == "observation.collection" && dec.Code == "background_result_missing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing result not recorded: %+v", tools[0].Decisions)
	}
}

func TestBackgroundRunsAreRebuiltFromStoredEvents(t *testing.T) {
	start := &event.Event{Seq: 3, Background: true, Execution: &event.Execution{ProcessID: "bg_a"}}
	finished := &event.Event{Seq: 4, Background: true, Execution: &event.Execution{ProcessID: "bg_b"}}
	done := &event.Event{Seq: 6, OriginSeq: 4}
	missed := &event.Event{Seq: 5, Background: true, Execution: &event.Execution{ProcessID: "bg_c"},
		Decisions: []event.Decision{{Rule: "observation.collection", Code: "background_result_missing"}}}
	s := &Session{}
	s.restoreBackground([]*event.Event{start, finished, missed, done})
	if len(s.background) != 1 || s.background["bg_a"] == nil {
		t.Fatalf("restored %v", s.background)
	}
}
