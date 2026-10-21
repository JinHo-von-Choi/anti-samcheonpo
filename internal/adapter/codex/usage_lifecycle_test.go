package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrackerResumeDuplicateAndModelChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	initial := `{"type":"turn_context","payload":{"model":"first"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":80,"output_tokens":20,"total_tokens":120}}}}
`
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker(path)
	if deltas, err := tracker.Poll(); err != nil || len(deltas) != 0 {
		t.Fatalf("resume charged old tokens: %v %v", deltas, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(initial + `{"type":"turn_context","payload":{"model":"second"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":100,"output_tokens":30,"total_tokens":180}}}}
`)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	deltas, err := tracker.Poll()
	if err != nil || len(deltas) != 1 || deltas[0].Model != "second" || deltas[0].In != 30 || deltas[0].CacheRead != 20 || deltas[0].Out != 10 {
		t.Fatalf("wrong delta/model: %+v %v", deltas, err)
	}
}
