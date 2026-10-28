package mcode

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// record writes a session folder the way mcode lays it out and returns the
// messages.jsonl path.
func record(t *testing.T, lines []map[string]any) string {
	t.Helper()
	data := t.TempDir()
	dir := filepath.Join(data, "v2", "sessions", "2026", "10", "28", "12-00-00-000-session_x")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"schemaVersion":1,"sessionId":"mvs_t"}`), 0o600)
	var b strings.Builder
	for _, l := range lines {
		raw, _ := json.Marshal(map[string]any{"message_id": "m", "turn_id": "t", "message": l})
		b.Write(raw)
		b.WriteByte('\n')
	}
	path := filepath.Join(dir, "messages.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func call(id, name string, args map[string]any, ts int64) map[string]any {
	return map[string]any{"role": "assistant", "timestamp": ts, "model": "MiniMax-M3.1-Flash",
		"usage":   map[string]any{"input": 100, "output": 10},
		"content": []any{map[string]any{"type": "toolCall", "id": id, "name": name, "arguments": args}}}
}

func result(id, name, text string, details map[string]any, ts int64) map[string]any {
	return map[string]any{"role": "toolResult", "toolCallId": id, "toolName": name, "timestamp": ts,
		"content": []any{map[string]any{"type": "text", "text": text}}, "details": details}
}

func shellEvents(s *event.Session) []*event.Event {
	var out []*event.Event
	for _, ev := range s.Events {
		if ev.Tool == event.ToolShell {
			out = append(out, ev)
		}
	}
	return out
}

func TestParseRecordShellResults(t *testing.T) {
	exit7 := map[string]any{"execution": map[string]any{"status": "failed", "reason": "exited", "exitCode": 7},
		"processOutput": map[string]any{"stdout": "", "stderr": "boom"}}
	path := record(t, []map[string]any{
		{"role": "user", "timestamp": 1000, "content": []any{map[string]any{"type": "text", "text": "<system-reminder>\nrules\n</system-reminder>\n\ncargo test 고쳐"}}},
		call("c1", "bash", map[string]any{"command": "cargo test"}, 2000),
		result("c1", "bash", "boom\n\nCommand exited with code 7", exit7, 9000),
		call("c2", "bash", map[string]any{"command": "cargo test", "run_in_background": true}, 10000),
		result("c2", "bash", `<bash_background task_id="bg_1">`+"\nPurpose: cargo test", map[string]any{}, 10100),
		call("c3", "task_output", map[string]any{"task_id": "bg_1"}, 20000),
		result("c3", "task_output", "<task_output status=\"succeeded\">ok</task_output>",
			map[string]any{"task_id": "bg_1", "status": "succeeded", "execution": map[string]any{"status": "succeeded", "reason": "exited", "exitCode": 0}}, 30000),
		{"role": "user", "timestamp": 31000, "content": []any{map[string]any{"type": "text", "text": "<system-reminder>\nonly a reminder\n</system-reminder>"}}},
	})
	s, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "mvs_t" || s.Agent != "mcode" || s.FirstPrompt != "cargo test 고쳐" {
		t.Fatalf("session id=%s agent=%s first=%q", s.ID, s.Agent, s.FirstPrompt)
	}
	sh := shellEvents(s)
	if len(sh) != 3 {
		t.Fatalf("want a failed run, a background start and its completion, got %d", len(sh))
	}
	if sh[0].ExitCode == nil || *sh[0].ExitCode != 7 || sh[0].DurationMS != 7000 {
		t.Fatalf("failed run exit=%v duration=%d", sh[0].ExitCode, sh[0].DurationMS)
	}
	if !sh[1].Background || sh[1].ExitCode != nil {
		t.Fatalf("background start bg=%v exit=%v", sh[1].Background, sh[1].ExitCode)
	}
	if sh[2].OriginSeq != sh[1].Seq || sh[2].ExitCode == nil || *sh[2].ExitCode != 0 || sh[2].Background {
		t.Fatalf("completion origin=%d/%d exit=%v", sh[2].OriginSeq, sh[1].Seq, sh[2].ExitCode)
	}
	var origins []string
	for _, ev := range s.Events {
		if ev.Kind == event.KindPrompt {
			origins = append(origins, ev.PromptOrigin)
		}
	}
	if strings.Join(origins, ",") != "user,harness" {
		t.Fatalf("prompt origins %v", origins)
	}
	if sh[0].Usage.In != 100 || s.Model != "MiniMax-M3.1-Flash" {
		t.Fatalf("usage %+v model %s", sh[0].Usage, s.Model)
	}
}

// TestParseRecordFillsUnreadTasksFromTheRuntimeDatabase covers a background
// run the agent never read: its result comes from the runtime database and
// lands in time order at the stated end.
func TestParseRecordFillsUnreadTasksFromTheRuntimeDatabase(t *testing.T) {
	path := record(t, []map[string]any{
		call("c1", "bash", map[string]any{"command": "npm test", "run_in_background": true}, 1000),
		result("c1", "bash", `<bash_background task_id="bg_9">`, map[string]any{}, 1100),
		call("c2", "read", map[string]any{"path": "/w/a.txt"}, 50000),
		result("c2", "read", "a", map[string]any{}, 50100),
		call("c3", "bash", map[string]any{"command": "sleep 1", "run_in_background": true}, 60000),
		result("c3", "bash", `<bash_background task_id="bg_lost">`, map[string]any{}, 60100),
	})
	db, err := sql.Open("sqlite", tasksPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(tasksPath(path)), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE local_runtime_background_tasks (task_id TEXT PRIMARY KEY, owner_session_id TEXT, kind TEXT, record_json TEXT)`); err != nil {
		t.Fatal(err)
	}
	rec := `{"taskId":"bg_9","status":"failed","toolCallId":"c1","startedAt":1050,"endedAt":20000,
		"metadata":{"executionMode":"explicit_background","workingDirectory":"/w",
		"bashDetails":{"execution":{"status":"failed","reason":"exited","exitCode":1},"processOutput":{"stdout":"","stderr":"1 failed"}}}}`
	if _, err := db.Exec(`INSERT INTO local_runtime_background_tasks VALUES ('bg_9','mvs_t','bash',?)`, rec); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.ProjectPath != "/w" {
		t.Fatalf("project path from the runtime database: %q", s.ProjectPath)
	}
	var kinds []string
	var done, lost *event.Event
	for _, ev := range s.Events {
		switch {
		case ev.OriginSeq > 0 || strings.HasSuffix(ev.CallID, "#result"):
			done = ev
			kinds = append(kinds, "done")
		case ev.Tool == event.ToolShell && ev.CallID == "c3":
			lost = ev
			kinds = append(kinds, "lost")
		case ev.Tool == event.ToolShell:
			kinds = append(kinds, "start")
		case ev.Kind == event.KindTool:
			kinds = append(kinds, ev.Tool)
		}
	}
	if strings.Join(kinds, ",") != "start,done,read,lost" {
		t.Fatalf("time order %v", kinds)
	}
	if done.ExitCode == nil || *done.ExitCode != 1 || done.Execution.ExitSource != "mcode_runtime_db" {
		t.Fatalf("completion %+v", done.Execution)
	}
	if s.Events[done.OriginSeq].CallID != "c1" {
		t.Fatalf("origin points at %s", s.Events[done.OriginSeq].CallID)
	}
	found := false
	for _, d := range lost.Decisions {
		found = found || d.Code == "background_result_missing"
	}
	if !found {
		t.Fatal("a run without any result is recorded as a gap")
	}
}

func TestUserTextDropsHarnessBlocks(t *testing.T) {
	for in, want := range map[string]string{
		"<system-reminder>\na\n</system-reminder>\n\n플랜 구현해":         "플랜 구현해",
		"<background-task-finished>\nx\n</background-task-finished>": "",
		"그냥 요청": "그냥 요청",
	} {
		if got := userText(in); got != want {
			t.Fatalf("%q -> %q want %q", in, got, want)
		}
	}
}
