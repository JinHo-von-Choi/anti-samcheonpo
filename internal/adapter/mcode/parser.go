package mcode

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/claude"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/bgrun"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// line is one record of a session's messages.jsonl.
type line struct {
	Message struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		Summary    string          `json:"summary"`
		Timestamp  json.RawMessage `json:"timestamp"`
		Model      string          `json:"model"`
		ToolCallID string          `json:"toolCallId"`
		ToolName   string          `json:"toolName"`
		Details    json.RawMessage `json:"details"`
		IsError    bool            `json:"isError"`
		Usage      *struct {
			Input      int64 `json:"input"`
			Output     int64 `json:"output"`
			CacheRead  int64 `json:"cacheRead"`
			CacheWrite int64 `json:"cacheWrite"`
		} `json:"usage"`
	} `json:"message"`
}

type part struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// stamp reads a timestamp stated as epoch milliseconds, as a number or a string.
func stamp(raw json.RawMessage) time.Time {
	s := strings.Trim(string(raw), `"`)
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil || ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// injectedRe matches the blocks the harness adds to a user turn.
var injectedRe = regexp.MustCompile(`(?s)<(system-reminder|background-task-finished|task-notification|agent-context)>.*?</(system-reminder|background-task-finished|task-notification|agent-context)>`)

// userText removes the harness blocks from a user turn. An empty result is
// a turn the harness wrote, not a request.
func userText(t string) string {
	return strings.TrimSpace(injectedRe.ReplaceAllString(t, ""))
}

// task is what the runtime database states about one shell call.
type task struct {
	ID, Status, Mode, Dir string
	Exec                  *Execution
	Stdout, Stderr        string
	Started, Ended        time.Time
}

// tasksPath finds the runtime database of a session record:
// <data>/v2/sessions/YYYY/MM/DD/<dir>/messages.jsonl -> <data>/v2/sqlite/runtime-state.sqlite.
func tasksPath(messages string) string {
	dir := filepath.Dir(messages)
	for i := 0; i < 4; i++ {
		dir = filepath.Dir(dir)
	}
	if filepath.Base(dir) != "sessions" {
		return ""
	}
	return filepath.Join(filepath.Dir(dir), "sqlite", "runtime-state.sqlite")
}

// loadTasks reads the shell tasks of one session by tool call id. A missing
// or locked database leaves the record alone as the only source.
func loadTasks(path, session string) (map[string]task, error) {
	if path == "" || session == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	// file:///C:/... on Windows, like the ledger opens its own database
	slashed := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(slashed, "//") {
		slashed = "/" + slashed
	}
	u := url.URL{Scheme: "file", Path: slashed, RawQuery: "mode=ro&_pragma=busy_timeout(200)&_pragma=query_only(1)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT record_json FROM local_runtime_background_tasks WHERE owner_session_id=? AND kind='bash'`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]task{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var r struct {
			TaskID     string `json:"taskId"`
			Status     string `json:"status"`
			ToolCallID string `json:"toolCallId"`
			StartedAt  int64  `json:"startedAt"`
			EndedAt    int64  `json:"endedAt"`
			Metadata   struct {
				ExecutionMode    string `json:"executionMode"`
				WorkingDirectory string `json:"workingDirectory"`
				BashDetails      struct {
					Execution     *Execution     `json:"execution"`
					ProcessOutput *ProcessOutput `json:"processOutput"`
				} `json:"bashDetails"`
			} `json:"metadata"`
		}
		if json.Unmarshal([]byte(raw), &r) != nil || r.ToolCallID == "" {
			continue
		}
		t := task{ID: r.TaskID, Status: r.Status, Mode: r.Metadata.ExecutionMode, Dir: r.Metadata.WorkingDirectory, Exec: r.Metadata.BashDetails.Execution}
		if po := r.Metadata.BashDetails.ProcessOutput; po != nil {
			t.Stdout, t.Stderr = po.Stdout, po.Stderr
		}
		if r.StartedAt > 0 {
			t.Started = time.UnixMilli(r.StartedAt)
		}
		if r.EndedAt > 0 {
			t.Ended = time.UnixMilli(r.EndedAt)
		}
		out[r.ToolCallID] = t
	}
	return out, rows.Err()
}

// workDir is the folder most shell calls of the session ran in.
func workDir(tasks map[string]task) string {
	count := map[string]int{}
	best := ""
	for _, t := range tasks {
		if t.Dir == "" {
			continue
		}
		count[t.Dir]++
		if count[t.Dir] > count[best] || count[t.Dir] == count[best] && t.Dir < best {
			best = t.Dir
		}
	}
	return best
}

// ParseFile parses a MiniMax Code session record (messages.jsonl). The
// session id comes from manifest.json next to it. Shell results the record
// lacks (background tasks the agent never read) come from the runtime
// database when it can be read.
func ParseFile(path string) (*event.Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var manifest struct {
		SessionID string `json:"sessionId"`
	}
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(path), "manifest.json")); err == nil {
		_ = json.Unmarshal(b, &manifest)
	}
	tasks, taskErr := loadTasks(tasksPath(path), manifest.SessionID)
	h := sha256.New()
	rd := bufio.NewReaderSize(io.TeeReader(f, h), 1<<20)
	b := newBuilder(path, manifest.SessionID, tasks)
	var buf []byte
	for {
		raw, err := claude.ReadLine(rd, &buf)
		if len(raw) > 0 {
			b.feed(raw)
		}
		if err != nil {
			break
		}
	}
	s := b.finish()
	s.SourceHash = hex.EncodeToString(h.Sum(nil))
	// a database that cannot be read (locked, another schema) leaves the
	// record as the only source: background results it never read stay
	// marked missing instead of failing the audit
	_ = taskErr
	return s, nil
}

// builder turns record lines into events with the parser the live hooks use.
type builder struct {
	p          *claude.Parser
	tasks      map[string]task
	calls      map[string]*event.Event // tool call id -> event
	callAt     map[string]time.Time
	background map[string]*event.Event // task id -> background start
	last       time.Time
}

func newBuilder(path, id string, tasks map[string]task) *builder {
	p := claude.NewParser(path)
	p.Session.ID = id
	p.Session.Agent = "mcode"
	p.Session.Mode = "audit"
	p.Session.SourcePath = path
	if root := workDir(tasks); root != "" {
		// the record states no project folder; the shell calls ran in one
		p.SetRoot(root)
		p.Cwd = root
		p.Session.ProjectPath = root
	}
	return &builder{p: p, tasks: tasks, calls: map[string]*event.Event{}, callAt: map[string]time.Time{}, background: map[string]*event.Event{}}
}

func (b *builder) feed(raw []byte) {
	var l line
	if json.Unmarshal(raw, &l) != nil {
		return
	}
	m := l.Message
	ts := stamp(m.Timestamp)
	if ts.IsZero() {
		ts = b.last
	}
	if !ts.IsZero() {
		b.last = ts
		if b.p.Session.StartedAt.IsZero() {
			b.p.Session.StartedAt = ts
		}
		b.p.Session.EndedAt = ts
	}
	var parts []part
	_ = json.Unmarshal(m.Content, &parts)
	switch m.Role {
	case "user":
		var text strings.Builder
		for _, x := range parts {
			if x.Type == "text" {
				text.WriteString(x.Text)
			}
		}
		t := userText(text.String())
		ev := &event.Event{Kind: event.KindPrompt, TS: ts, Summary: "prompt", Text: t, PromptOrigin: "user"}
		if t == "" {
			ev.PromptOrigin = "harness"
		} else if b.p.Session.FirstPrompt == "" {
			b.p.Session.FirstPrompt = t
		}
		b.p.AddEvent(ev)
	case "compactionSummary":
		b.p.AddEvent(&event.Event{Kind: event.KindCompact, TS: ts, Summary: "compact"})
	case "assistant":
		if m.Model != "" {
			b.p.Session.Model = m.Model
		}
		var tools []*event.Event
		var msg *event.Event
		for _, x := range parts {
			switch x.Type {
			case "text":
				if strings.TrimSpace(x.Text) == "" {
					continue
				}
				if msg == nil {
					msg = &event.Event{Kind: event.KindMessage, TS: ts, Summary: "message"}
					b.p.AddEvent(msg)
				}
				msg.Text += x.Text
			case "toolCall":
				if TaskTool(x.Name) || x.ID == "" {
					continue
				}
				ev := b.p.HookToolUse(Tool(x.Name), x.ID, Input(x.Arguments), ts)
				b.calls[x.ID] = ev
				b.callAt[x.ID] = ts
				tools = append(tools, ev)
			}
		}
		if m.Usage != nil {
			u := event.Usage{In: m.Usage.Input, Out: m.Usage.Output, CacheRead: m.Usage.CacheRead, CacheWrite: m.Usage.CacheWrite, Model: m.Model}
			spread(u, tools, msg)
		}
	case "toolResult":
		r := Response{}
		_ = json.Unmarshal(m.Content, &r.Content)
		_ = json.Unmarshal(m.Details, &r.Details)
		if TaskTool(m.ToolName) {
			for _, t := range ReadTasks(&r) {
				b.complete(t.ID, t.Status, t.Result, ts, "background_result")
			}
			return
		}
		ev := b.calls[m.ToolCallID]
		if ev == nil {
			return
		}
		res := ReadResult(ev.RawTool, &r)
		if ev.RawTool == "" {
			res = ReadResult(Tool(m.ToolName), &r)
		}
		resp, _ := json.Marshal(map[string]any{"stdout": res.Text})
		b.p.HookToolResult(m.ToolCallID, resp, m.IsError || res.State == "failed" || res.State == "timeout", "")
		b.apply(ev, m.ToolCallID, res, ts)
	}
}

// spread splits a message's usage evenly over its calls (remainder to the
// first), like the Claude parser; a message without calls keeps it.
func spread(u event.Usage, tools []*event.Event, msg *event.Event) {
	if len(tools) == 0 {
		if msg != nil {
			msg.Usage.Add(u)
		}
		return
	}
	n := int64(len(tools))
	for i, ev := range tools {
		part := event.Usage{In: u.In / n, Out: u.Out / n, CacheRead: u.CacheRead / n, CacheWrite: u.CacheWrite / n, Model: u.Model}
		if i == 0 {
			part.In += u.In % n
			part.Out += u.Out % n
			part.CacheRead += u.CacheRead % n
			part.CacheWrite += u.CacheWrite % n
		}
		ev.Usage.Add(part)
	}
}

// apply records a call's stated result. The runtime database fills a shell
// exit the record does not state.
func (b *builder) apply(ev *event.Event, id string, res Result, ts time.Time) {
	ev.ResultObserved = true
	if at := b.callAt[id]; !at.IsZero() && ts.After(at) {
		ev.DurationMS = ts.Sub(at).Milliseconds()
	}
	if ev.Tool != event.ToolShell {
		return
	}
	if t, ok := b.tasks[id]; ok && res.State == "unknown" {
		res.State, res.Failure, res.Exit = t.Exec.State()
	}
	bgrun.SetShellResult(ev, res.Exit, res.Text)
	if res.State == "background" {
		ev.Background = true
		ev.ExitCode = nil
		ev.IsError = false
		if res.TaskID != "" {
			b.background[res.TaskID] = ev
		}
	}
	if t, ok := b.tasks[id]; ok && !t.Started.IsZero() && !t.Ended.IsZero() && res.State != "background" {
		ev.DurationMS = t.Ended.Sub(t.Started).Milliseconds()
	}
	ev.Execution = &event.Execution{State: res.State, ExitSource: "mcode_record", CallID: id, ProcessID: res.TaskID, FailureType: res.Failure}
}

// complete adds the completion event of a background start.
func (b *builder) complete(taskID, status string, res Result, ts time.Time, source string) {
	start := b.background[taskID]
	if start == nil || !Terminal(status) {
		return
	}
	delete(b.background, taskID)
	ended := time.Time{}
	if t, ok := b.tasks[start.CallID]; ok {
		ended = t.Ended
	}
	o := bgrun.Outcome{State: res.State, Failure: res.Failure, Exit: res.Exit, Text: res.Text, TaskID: taskID, Source: source}
	b.p.AddEvent(bgrun.Completion(start, o, ended, ts))
}

// finish completes background starts the record never read from the
// runtime database, marks the rest as missing, and orders events in time.
func (b *builder) finish() *event.Session {
	ids := make([]string, 0, len(b.background))
	for id := range b.background {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		start := b.background[id]
		if t, ok := b.tasks[start.CallID]; ok && Terminal(t.Status) {
			res := Result{TaskID: id, Text: strings.TrimSpace(t.Stdout + "\n" + t.Stderr)}
			res.State, res.Failure, res.Exit = t.Exec.State()
			now := t.Ended
			if now.IsZero() {
				now = b.last
			}
			b.complete(id, t.Status, res, now, "mcode_runtime_db")
			continue
		}
		start.Decisions = append(start.Decisions, event.Decision{Rule: "observation.collection", Code: "background_result_missing", Evidence: []int64{start.Seq}})
	}
	s := b.p.Finish()
	// completions read from the database land at their stated end; the
	// rules read events in time order, so renumber after sorting
	sort.SliceStable(s.Events, func(i, j int) bool { return s.Events[i].TS.Before(s.Events[j].TS) })
	renum := map[int64]int64{}
	for i, ev := range s.Events {
		renum[ev.Seq] = int64(i)
	}
	for i, ev := range s.Events {
		ev.Seq = int64(i)
		ev.SessionID = s.ID
		if strings.HasSuffix(ev.CallID, "#result") {
			ev.OriginSeq = renum[ev.OriginSeq]
		}
		for k := range ev.Decisions {
			for j, x := range ev.Decisions[k].Evidence {
				if n, ok := renum[x]; ok {
					ev.Decisions[k].Evidence[j] = n
				}
			}
		}
	}
	return s
}
