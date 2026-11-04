package live

import (
	"encoding/json"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeNativeChildIsSeparateFromParent(t *testing.T) {
	raw := json.RawMessage(`{"session_id":"parent","agent_id":"child","cwd":"/w/proj","tool_name":"Write","tool_use_id":"write","tool_input":{"file_path":"/w/proj/a.txt","content":"a"}}`)
	names, ins, err := claudeIn("PreToolUse", raw)
	if err != nil || len(names) != 1 || ins[0].SessionID != nativeChildSessionID("parent", "child") || ins[0].ParentSessionID != "parent" {
		t.Fatalf("%v %+v %v", names, ins, err)
	}
	if strings.ContainsAny(ins[0].SessionID, ":/\\") {
		t.Fatal("child ID is not a portable receipt filename")
	}
	names, ins, err = claudeIn("SubagentStop", raw)
	if err != nil || names[0] != "Stop" || !ins[0].ChildStop {
		t.Fatalf("%v %+v %v", names, ins, err)
	}
}

func TestNativeParentBudgetRestoresBeforeAnyChildResumes(t *testing.T) {
	parent, d := reliabilitySession(t)
	parent.onPrompt(HookInput{Prompt: "기능 구현해"})
	if err := os.WriteFile(filepath.Join(parent.Root, ".samcheonpo.yml"), []byte("notify: {desktop: false}\niron_laws: {enabled: false}\nswarm: {tree_krw: 1}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	child, err := newSession("native-child", "claude", parent.Root, "", parent.db, parent.prices, adapter.Profiles["claude"].Caps)
	if err != nil {
		t.Fatal(err)
	}
	child.onPrompt(HookInput{Prompt: "하위 기능 구현해"})
	if err := parent.db.BindSessionParent("claude", child.ID, parent.ID, intent.ProjectID(parent.Root), "native"); err != nil {
		t.Fatal(err)
	}
	var usage HookInput
	if err := json.Unmarshal([]byte(`{"message_id":"child-cost","model":"claude-sonnet-5-5","usage":{"input_tokens":1000,"output_tokens":2000}}`), &usage); err != nil {
		t.Fatal(err)
	}
	child.onUsage(usage)
	if _, err := child.Finalize(); err != nil {
		t.Fatal(err)
	}
	nextDaemon := &Daemon{sessions: map[string]*Session{}, db: d.db, prices: d.prices, versions: map[string]string{"claude": "2.1.0"}}
	resumed, err := nextDaemon.session(parent.ID, "claude", parent.Root, "")
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Finalize()
	if _, _, blocked, bounded := nextDaemon.swarm.state(parent.ID); !bounded || !blocked {
		t.Fatal("resumed parent forgot a closed child's spend")
	}
	before, _, _, _ := nextDaemon.swarm.state(parent.ID)
	nextDaemon.mu.Lock()
	err = nextDaemon.restoreNativeTreeLocked(resumed)
	nextDaemon.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	after, _, _, _ := nextDaemon.swarm.state(parent.ID)
	if before != after {
		t.Fatal("lineage replay double counted spend")
	}
	alone, err := nextDaemon.session("unrelated", "claude", parent.Root, "")
	if err != nil {
		t.Fatal(err)
	}
	defer alone.Finalize()
	if _, _, blocked, bounded := nextDaemon.swarm.state(alone.ID); blocked || bounded {
		t.Fatal("unrelated tree inherited child budget")
	}
}

func TestNativeChildNeverReadsRootTranscriptUsage(t *testing.T) {
	_, ins, err := claudeIn("PreToolUse", json.RawMessage(`{"session_id":"parent","agent_id":"child","transcript_path":"/private/root.jsonl","tool_name":"Read"}`))
	if err != nil || ins[0].TranscriptPath != "" {
		t.Fatal("root usage was assigned to child")
	}
	_, ins, err = claudeIn("SubagentStop", json.RawMessage(`{"session_id":"parent","agent_id":"child","transcript_path":"/private/root.jsonl","agent_transcript_path":"/private/child.jsonl"}`))
	if err != nil || ins[0].TranscriptPath != "/private/child.jsonl" {
		t.Fatal("explicit child usage was lost")
	}
}

func TestChildCompletionCannotClearParentsUnverifiedWork(t *testing.T) {
	parent, d := reliabilitySession(t)
	parent.onPrompt(HookInput{Prompt: "부모 모듈과 하위 모듈 구현해"})
	feedWrite(t, parent, "parent-write", "parent/main.go")
	id := nativeChildSessionID(parent.ID, "child")
	_, child, err := d.dispatch("claude", "SessionStart", HookInput{SessionID: id, ParentSessionID: parent.ID, Cwd: parent.Root})
	if err != nil {
		t.Fatal(err)
	}
	child.onPrompt(HookInput{Prompt: "하위 모듈 구현해"})
	feedWrite(t, child, "child-write", "child/part.go")
	feedCheck(t, child, "child-check", "go test ./child", 0)
	if !child.verified() {
		t.Fatal("child check did not verify child")
	}
	if parent.verified() {
		t.Fatal("child's passing check verified unrelated parent change")
	}
	_, _, err = d.dispatch("claude", "Stop", HookInput{SessionID: id, ParentSessionID: parent.ID, Cwd: parent.Root, ChildStop: true, LastMessage: "하위 모듈 검사 통과"})
	if err != nil {
		t.Fatal(err)
	}
	if parent.closed || parent.verified() {
		t.Fatal("child completion finalized or accepted its parent")
	}
}

func TestChildWithoutRootIdentityIsNotInvented(t *testing.T) {
	_, ins, err := claudeIn("PreToolUse", json.RawMessage(`{"agent_id":"child","tool_name":"Write"}`))
	if err != nil || ins[0].SessionID != "" || ins[0].ParentSessionID != "" {
		t.Fatal("unidentified child was assigned a trusted synthetic root")
	}
}
