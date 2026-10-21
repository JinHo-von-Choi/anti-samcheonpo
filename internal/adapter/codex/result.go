package codex

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

var runningID = regexp.MustCompile(`(?im)^Process running with session ID\s+([0-9]+)`)
var explicitExit = regexp.MustCompile(`(?im)^(?:Process exited with code|Exit code:?)\s*(-?\d+)\s*$`)

// ResponseResult reads execution metadata, never absence of error text.
func ResponseResult(raw json.RawMessage) (code *int, process string, state string) {
	state = "unknown"
	var text string
	if json.Unmarshal(raw, &text) == nil {
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(text), &object) == nil && object != nil {
			return ResponseResult(json.RawMessage(text))
		}
		// Process output may itself print words resembling transport metadata.
		for _, marker := range []string{"\nOutput:", "\nFinal output:"} {
			if i := strings.Index(text, marker); i >= 0 {
				text = text[:i]
			}
		}
		if m := runningID.FindStringSubmatch(text); m != nil {
			return nil, m[1], "running"
		}
		if m := explicitExit.FindStringSubmatch(text); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return &n, "", "exited"
			}
		}
		if strings.HasPrefix(text, "Command interrupted") || strings.HasPrefix(text, "Process interrupted") {
			state = "interrupted"
		}
		return nil, "", state
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, "", state
	}
	if value := object["exit_code"]; len(value) > 0 && string(value) != "null" {
		var n int
		if json.Unmarshal(value, &n) == nil {
			return &n, "", "exited"
		}
	}
	if value := object["metadata"]; len(value) > 0 {
		if n, id, s := ResponseResult(value); n != nil || s != "unknown" {
			return n, id, s
		}
	}
	if value := object["session_id"]; len(value) > 0 && string(value) != "null" {
		var n json.Number
		if json.Unmarshal(value, &n) == nil {
			return nil, n.String(), "running"
		}
	}
	var interrupted bool
	if json.Unmarshal(object["interrupted"], &interrupted) == nil && interrupted {
		return nil, "", "interrupted"
	}
	return nil, "", state
}

// ApplyResponse replaces inferred shell success with observed result metadata.
func ApplyResponse(ev *event.Event, raw json.RawMessage) {
	if ev.Tool != event.ToolShell {
		return
	}
	code, process, state := ResponseResult(raw)
	if code == nil && ev.Execution != nil && ev.Execution.State == "exited" {
		return // a later transport-only response cannot erase a known completion
	}
	ev.ExitCode = code
	ev.Execution = &event.Execution{State: state, ProcessID: process, CallID: ev.CallID, ObservedAt: time.Now(), SourceDigest: fp.Hash(string(raw))}
	if code != nil {
		ev.Execution.ExitSource = "tool_result"
		ev.IsError = *code != 0
	}
	ev.Background = state == "running"
	ev.ResultFP = fp.ResultFP(ev.ExitCode, ev.ErrFPs, ev.FailedTests)
}
