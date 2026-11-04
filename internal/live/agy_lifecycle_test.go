package live

import (
	"encoding/json"
	"testing"
)

func TestAgySessionStartAndActiveBackgroundStop(t *testing.T) {
	raw := json.RawMessage(`{"conversationId":"agy-lifecycle","workspacePaths":["/w/proj"],"modelName":"configured-model"}`)
	names, ins, err := agyIn("SessionStart", raw)
	if err != nil || len(names) != 1 || names[0] != "SessionStart" || ins[0].SessionID != "agy-lifecycle" || ins[0].Model != "configured-model" {
		t.Fatalf("start: %v %+v %v", names, ins, err)
	}
	post := json.RawMessage(`{"conversationId":"agy-lifecycle","workspacePaths":["/w/proj"],"stepIdx":2,"toolCall":{"name":"run_command","args":{"CommandLine":"sleep 30"}}}`)
	if _, _, err := agyIn("PostToolUse", post); err != nil {
		t.Fatal(err)
	}
	names, _, err = agyIn("Stop", json.RawMessage(`{"conversationId":"agy-lifecycle","workspacePaths":["/w/proj"],"terminationReason":"model_stop","fullyIdle":false}`))
	if err != nil || len(names) != 0 {
		t.Fatalf("active background became completion checkpoint: %v %v", names, err)
	}
	agyHeld.Lock()
	pending := len(agyHeld.m["agy-lifecycle"])
	agyHeld.Unlock()
	if pending != 1 {
		t.Fatal("pending result discarded")
	}
	names, ins, err = agyIn("Stop", json.RawMessage(`{"conversationId":"agy-lifecycle","workspacePaths":["/w/proj"],"terminationReason":"model_stop","fullyIdle":true}`))
	if err != nil || len(names) != 2 || names[0] != "PostToolUse" || names[1] != "Stop" || ins[0].Error != "" {
		t.Fatalf("idle checkpoint: %v %+v %v", names, ins, err)
	}
}

func TestAgyCurrentWriteFileTracksPath(t *testing.T) {
	names, ins, err := agyIn("PreToolUse", json.RawMessage(`{"conversationId":"agy-write","workspacePaths":["/w/proj"],"stepIdx":2,"toolCall":{"name":"write_file","args":{"Path":"/w/proj/result.txt","Content":"ok"}}}`))
	if err != nil || len(names) != 1 || names[0] != "PreToolUse" || ins[0].ToolName != "Write" {
		t.Fatalf("%v %+v %v", names, ins, err)
	}
	var input map[string]string
	if err := json.Unmarshal(ins[0].ToolInput, &input); err != nil || input["file_path"] != "/w/proj/result.txt" || input["content"] != "ok" {
		t.Fatalf("write tracking lost: %s", ins[0].ToolInput)
	}
}

// Real asynchronous status contains DONE and task output but no process exit
// code. Neither starting the task nor reading its status proves a check passed.
func TestAgyBackgroundStatusDoesNotInventExitCode(t *testing.T) {
	transcript := "../../testdata/hooks/agy/1.4.12/background-transcript.jsonl"
	for _, step := range []int{3, 6} {
		out, _, known := agyStepResult(transcript, step)
		if out == "" || known {
			t.Fatalf("step %d became exit evidence: %q known=%v", step, out, known)
		}
	}
}
