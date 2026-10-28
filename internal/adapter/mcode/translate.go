// Package mcode reads MiniMax Code tool calls and results, from its hooks
// and from its session records. Both state a result in the same shape
// (testdata/hooks/mcode/README.md).
package mcode

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Tool maps a MiniMax Code tool name to the Claude tool name the parsers read.
func Tool(name string) string {
	switch name {
	case "bash":
		return "Bash"
	case "read":
		return "Read"
	case "write":
		return "Write"
	case "edit":
		return "Edit"
	case "grep":
		return "Grep"
	case "glob", "ls":
		return "Glob"
	case "web_fetch":
		return "WebFetch"
	case "web_search":
		return "WebSearch"
	case "ask_user":
		return "AskUserQuestion"
	case "task":
		return "Agent"
	}
	return name
}

// TaskTool reports the tools that read or stop a background task. They are
// not calls of their own: their results complete an earlier shell call.
func TaskTool(name string) bool {
	return name == "task_output" || name == "task_query" || name == "task_stop"
}

// Input renames the path field write and read state ("path") to the one the
// parser reads ("file_path"); edit already states file_path.
func Input(raw json.RawMessage) json.RawMessage {
	var input map[string]json.RawMessage
	if json.Unmarshal(raw, &input) != nil || input == nil {
		return raw
	}
	if _, ok := input["file_path"]; !ok {
		if p, ok := input["path"]; ok {
			input["file_path"] = p
		}
	}
	b, _ := json.Marshal(input)
	return b
}

// Execution is the process outcome a shell or task result states.
type Execution struct {
	Status   string `json:"status"`
	Reason   string `json:"reason"`
	ExitCode *int   `json:"exitCode"`
}

// State reads a stated execution: exited | failed | timeout | canceled |
// unknown, the failure type, and the exit code when one is stated.
func (x *Execution) State() (string, string, *int) {
	if x == nil {
		return "unknown", "", nil
	}
	switch {
	case x.Reason == "command_timeout":
		return "timeout", "timeout", nil
	case x.Status == "canceled" || x.Status == "cancelled" || x.Reason == "canceled":
		return "canceled", "canceled", x.ExitCode
	case x.ExitCode == nil:
		return "unknown", "", nil
	case *x.ExitCode != 0:
		return "failed", "nonzero_exit", x.ExitCode
	}
	return "exited", "", x.ExitCode
}

// ProcessOutput is a shell result's output.
type ProcessOutput struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// Details is the structured part of a tool result.
type Details struct {
	Execution     *Execution     `json:"execution"`
	ProcessOutput *ProcessOutput `json:"processOutput"`
	TaskID        string         `json:"task_id"`
	Status        string         `json:"status"`
	Tasks         []struct {
		TaskID    string     `json:"task_id"`
		Kind      string     `json:"kind"`
		Status    string     `json:"status"`
		Execution *Execution `json:"execution"`
	} `json:"tasks"`
}

// Response is a tool result: a hook's tool_response, or the content and
// details of a session record's toolResult line.
type Response struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Details Details `json:"details"`
}

// Text is the model facing text of a result.
func (r *Response) Text() string {
	var b strings.Builder
	for _, c := range r.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// backgroundRe reads the task id of a shell call the CLI moved to the
// background; the call returns this placeholder instead of a result.
var backgroundRe = regexp.MustCompile(`^\s*<bash_background task_id="([^"]+)"`)

// childRe reads the session a finished subagent call ran in.
var childRe = regexp.MustCompile(`<task_result task_id="[^"]*" session_id="([^"]+)"`)

// Result is what a tool result states about its call.
type Result struct {
	State   string // exited | failed | timeout | canceled | background | unknown
	Failure string
	Exit    *int
	Text    string // the output the rules read
	TaskID  string // the background task a placeholder names
	Child   string // the session a finished subagent call names
}

// ReadResult reads the result of a call of a Claude-named tool.
func ReadResult(tool string, r *Response) Result {
	text := r.Text()
	switch tool {
	case "Bash":
		if m := backgroundRe.FindStringSubmatch(text); m != nil {
			return Result{State: "background", TaskID: m[1], Text: text}
		}
		res := Result{Text: text}
		res.State, res.Failure, res.Exit = r.Details.Execution.State()
		if po := r.Details.ProcessOutput; po != nil {
			res.Text = strings.TrimSpace(po.Stdout + "\n" + po.Stderr)
		}
		return res
	case "Agent":
		res := Result{State: "exited", Text: text}
		if m := childRe.FindStringSubmatch(text); m != nil {
			res.Child = m[1]
		}
		return res
	}
	return Result{State: "exited", Text: text}
}

// Task is a background task state a task tool result states.
type Task struct {
	ID, Status string
	Result     Result
}

// ReadTasks reads the task states of a task_output, task_query or task_stop
// result. Only shell tasks are kept.
func ReadTasks(r *Response) []Task {
	var out []Task
	add := func(id, status string, x *Execution, text string) {
		if id == "" {
			return
		}
		t := Task{ID: id, Status: status, Result: Result{Text: text, TaskID: id}}
		t.Result.State, t.Result.Failure, t.Result.Exit = x.State()
		out = append(out, t)
	}
	if r.Details.TaskID != "" {
		add(r.Details.TaskID, r.Details.Status, r.Details.Execution, r.Text())
	}
	for _, t := range r.Details.Tasks {
		if t.Kind == "" || t.Kind == "bash" {
			add(t.TaskID, t.Status, t.Execution, "")
		}
	}
	return out
}

// Terminal reports a background task status that will not change.
func Terminal(status string) bool {
	switch status {
	case "succeeded", "failed", "canceled", "cancelled", "timed_out":
		return true
	}
	return false
}
