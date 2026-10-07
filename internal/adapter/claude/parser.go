// Package claude parses Claude Code transcript JSONL into common events.
package claude

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/patch"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rules"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testout"
)

// NormalizeTool maps Claude Code tool names to common names.
func NormalizeTool(name string) string {
	switch name {
	case "Bash", "BashOutput", "PowerShell":
		return event.ToolShell
	case "Read":
		return event.ToolRead
	case "Write":
		return event.ToolWrite
	case "Edit", "MultiEdit", "NotebookEdit":
		return event.ToolEdit
	case "Grep", "Glob", "LS", "ToolSearch":
		return event.ToolSearch
	case "WebFetch", "WebSearch":
		return event.ToolWeb
	case "TodoWrite", "TaskCreate", "TaskUpdate":
		return event.ToolTodo
	case "Task", "Agent":
		return event.ToolTask
	case "apply_patch":
		return event.ToolEdit
	}
	return event.ToolOther
}

type rawLine struct {
	Type        string          `json:"type"`
	Subtype     string          `json:"subtype"`
	UUID        string          `json:"uuid"`
	Timestamp   string          `json:"timestamp"`
	SessionID   string          `json:"sessionId"`
	Cwd         string          `json:"cwd"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Message     *rawMessage     `json:"message"`
	ToolUse     json.RawMessage `json:"toolUseResult"`
	TotalCost   float64         `json:"totalCostUSD"`
}

type rawMessage struct {
	ID      string      `json:"id"`
	Model   string      `json:"model"`
	Role    string      `json:"role"`
	Content messageBody `json:"content"`
	Usage   *rawUsage   `json:"usage"`
}

// messageBody is a message content that is either a plain string or a list
// of blocks; decoding it in place keeps each transcript line to one pass.
type messageBody struct {
	Text   string
	Blocks []block
	IsText bool
}

// UnmarshalJSON decodes a string or a block list.
func (b *messageBody) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		b.IsText = true
		return json.Unmarshal(data, &b.Text)
	}
	return json.Unmarshal(data, &b.Blocks)
}

type rawUsage struct {
	In         int64 `json:"input_tokens"`
	Out        int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
	Creation   *struct {
		Eph1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

func (u *rawUsage) oneHour() int64 {
	if u.Creation == nil {
		return 0
	}
	return u.Creation.Eph1h
}

type block struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Text      string          `json:"text"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

type pendingMsg struct {
	id     string
	model  string
	usage  event.Usage
	events []*event.Event
	text   *event.Event
}

// Parser incrementally turns transcript lines into events.
type Parser struct {
	Session  *event.Session
	Root     string
	nextSeq  int64
	byCallID map[string]*event.Event
	cur      *pendingMsg
	seenMsg  map[string]bool
	// virtual workspace state for retrospective mode (path -> content hash)
	files map[string]string
	// last known content for edit application (bounded by size)
	contents map[string]string
	shellMut int
	// Cwd is the shell's directory as last reported (transcript line or hook).
	Cwd       string
	source    string
	sidechain bool
}

// NewParser creates a parser for a transcript file.
func NewParser(source string) *Parser {
	return &Parser{
		Session:  &event.Session{Agent: "claude", Mode: "audit", SourcePath: source},
		byCallID: map[string]*event.Event{},
		seenMsg:  map[string]bool{},
		files:    map[string]string{},
		contents: map[string]string{},
		source:   source,
	}
}

func hashStr(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16])
}

var exitRe = lazyre.New(`(?m)^(?:Error: )?Exit code (\d+)`)

// Feed consumes one JSONL line at byte offset off.
func (p *Parser) Feed(line []byte, off int64) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var r rawLine
	if err := json.Unmarshal(line, &r); err != nil {
		return
	}
	ts, _ := time.Parse(time.RFC3339Nano, r.Timestamp)
	if p.Session.ID == "" && r.SessionID != "" {
		p.Session.ID = r.SessionID
	}
	if p.Root == "" && r.Cwd != "" {
		p.Root = r.Cwd
		p.Session.ProjectPath = r.Cwd
	}
	if r.Cwd != "" {
		p.Cwd = r.Cwd
	}
	if !ts.IsZero() {
		if p.Session.StartedAt.IsZero() || ts.Before(p.Session.StartedAt) {
			p.Session.StartedAt = ts
		}
		if ts.After(p.Session.EndedAt) {
			p.Session.EndedAt = ts
		}
	}
	ref := fmt.Sprintf("%s#%d", p.source, off)
	switch r.Type {
	case "assistant":
		p.assistant(r, ts, ref)
	case "user":
		p.user(r, ts, ref)
	case "system":
		if r.Subtype == "compact_boundary" {
			p.flush()
			p.add(&event.Event{Kind: event.KindCompact, TS: ts, SourceRef: ref, Summary: "compact"})
		}
	case "cost-state":
		if r.TotalCost > p.Session.ReportedCostUSD {
			p.Session.ReportedCostUSD = r.TotalCost
		}
	}
}

func (p *Parser) add(ev *event.Event) {
	ev.SessionID = p.Session.ID
	ev.Seq = p.nextSeq
	ev.Basis = "hindsight"
	ev.Parent = ev.Parent || p.sidechain
	p.nextSeq++
	p.Session.Events = append(p.Session.Events, ev)
}

func (p *Parser) assistant(r rawLine, ts time.Time, ref string) {
	if r.Message == nil {
		return
	}
	m := *r.Message
	if m.ID == "" {
		m.ID = r.UUID
	}
	if p.cur == nil || p.cur.id != m.ID {
		p.flush()
		p.cur = &pendingMsg{id: m.ID, model: m.Model}
	}
	if m.Model != "" && m.Model != "<synthetic>" {
		p.cur.model = m.Model
		if p.Session.Model == "" {
			p.Session.Model = m.Model
		}
	}
	if m.Usage != nil {
		p.Session.UsageLines++
		p.Session.UsageParsed++
		u := m.Usage
		// element-wise max: repeated lines of one message carry the same usage
		p.cur.usage.In = max(p.cur.usage.In, u.In)
		p.cur.usage.Out = max(p.cur.usage.Out, u.Out)
		p.cur.usage.CacheRead = max(p.cur.usage.CacheRead, u.CacheRead)
		p.cur.usage.CacheWrite = max(p.cur.usage.CacheWrite, u.CacheWrite)
		p.cur.usage.CacheWrite1h = max(p.cur.usage.CacheWrite1h, u.oneHour())
	} else if m.Model != "<synthetic>" {
		p.Session.UsageLines++
	}
	for _, b := range m.Content.Blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			if p.cur.text == nil {
				p.cur.text = &event.Event{Kind: event.KindMessage, TS: ts, SourceRef: ref, Summary: "message"}
				p.add(p.cur.text)
			}
			p.cur.text.Text += b.Text
		case "tool_use":
			ev := p.toolUse(b, ts, ref, r.IsSidechain)
			p.Session.ToolUses++
			p.byCallID[b.ID] = ev
			p.cur.events = append(p.cur.events, ev)
		}
	}
}

// flush distributes the pending message's usage over its tool events.
func (p *Parser) flush() {
	c := p.cur
	p.cur = nil
	if c == nil || p.seenMsg[c.id] {
		return
	}
	p.seenMsg[c.id] = true
	c.usage.Model = c.model
	targets := c.events
	if len(targets) == 0 {
		if c.text == nil {
			c.text = &event.Event{Kind: event.KindMessage, Summary: "message"}
			if len(p.Session.Events) > 0 {
				c.text.TS = p.Session.Events[len(p.Session.Events)-1].TS
			}
			p.add(c.text)
		}
		targets = []*event.Event{c.text}
	}
	n := int64(len(targets))
	split := func(v int64) (int64, int64) { return v / n, v % n }
	in, inR := split(c.usage.In)
	out, outR := split(c.usage.Out)
	cr, crR := split(c.usage.CacheRead)
	cw, cwR := split(c.usage.CacheWrite)
	cw1, cw1R := split(c.usage.CacheWrite1h)
	for i, ev := range targets {
		u := event.Usage{In: in, Out: out, CacheRead: cr, CacheWrite: cw, CacheWrite1h: cw1, Model: c.model}
		if i == 0 {
			u.In += inR
			u.Out += outR
			u.CacheRead += crR
			u.CacheWrite += cwR
			u.CacheWrite1h += cw1R
		}
		ev.Usage.Add(u)
		if ev.Usage.Model == "" {
			ev.Usage.Model = c.model
		}
	}
	if c.text != nil && len(c.events) > 0 && c.text.Usage.Model == "" {
		c.text.Usage.Model = c.model
	}
}

// shellDir is the shell's directory relative to the project root. A
// directory outside the root keeps its absolute path, which never matches a
// run inside the project.
func (p *Parser) shellDir() string {
	if p.Cwd == "" || p.Root == "" {
		return ""
	}
	if r := p.rel(p.Cwd); r != "." {
		return r
	}
	return ""
}

func (p *Parser) rel(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "/") && !strings.HasPrefix(p.Root, "/") && filepath.Separator != '/' {
		// a Unix transcript read on another host: slash semantics, not the
		// host's
		return relSlash(filepath.ToSlash(p.Root), path)
	}
	path = filepath.Clean(path)
	if p.Root != "" && filepath.IsAbs(path) {
		if r, err := filepath.Rel(p.Root, path); err == nil && !strings.HasPrefix(r, "..") {
			return filepath.ToSlash(r)
		}
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(path)
}

// relSlash is rel for slash-separated absolute paths regardless of host.
func relSlash(root, p string) string {
	p = pathpkg.Clean(p)
	if root == "" {
		return p
	}
	root = pathpkg.Clean(root)
	if p == root {
		return "."
	}
	if strings.HasPrefix(p, root+"/") {
		return strings.TrimPrefix(p, root+"/")
	}
	return p
}

func (p *Parser) toolUse(b block, ts time.Time, ref string, side bool) *event.Event {
	ev := &event.Event{Kind: event.KindTool, TS: ts, RawTool: b.Name, Tool: NormalizeTool(b.Name), CallID: b.ID, SourceRef: ref, Parent: side}
	if strings.HasPrefix(b.Name, "mcp__") {
		ev.Tool = event.ToolOther
	}
	var in map[string]any
	_ = json.Unmarshal(b.Input, &in)
	str := func(k string) string {
		if v, ok := in[k].(string); ok {
			return v
		}
		return ""
	}
	switch ev.Tool {
	case event.ToolShell:
		ev.Cmd = str("command")
		ev.CmdNorm, ev.Dir = fp.NormalizeCmd(ev.Cmd)
		ev.CmdFP = fp.CmdFP(ev.CmdNorm)
		dir := p.shellDir()
		if wd := str("workdir"); wd != "" {
			// hosts that pass the directory with the call (Codex exec_command)
			if dir = p.rel(wd); dir == "." {
				dir = ""
			}
		}
		ev.ExecFP, ev.ExecCertain = fp.ExecFP(ev.Cmd, dir)
		ev.Summary = "shell: " + trunc(ev.CmdNorm, 100)
	case event.ToolRead:
		ev.Paths = []string{p.rel(str("file_path"))}
		ev.Summary = "read " + ev.Paths[0]
	case event.ToolSearch:
		if s := str("path"); s != "" {
			ev.Paths = []string{p.rel(s)}
		}
		ev.Summary = strings.ToLower(b.Name) + " " + trunc(str("pattern"), 60)
	case event.ToolWrite:
		path := p.rel(str("file_path"))
		ev.Paths = []string{path}
		content := str("content")
		h := hashStr(content)
		ev.WriteHashes = map[string]string{path: h}
		added := lines(content)
		var removed []string
		if old, ok := p.contents[path]; ok {
			added, removed = lineDiff(lines(old), added)
		}
		ev.Patch = []event.PatchFile{{Path: path, Added: added, Removed: removed}}
		ev.AddedLines = map[string]int{path: len(added)}
		ev.RemovedLines = map[string]int{path: len(removed)}
		p.setContent(path, content, h)
		ev.Summary = "write " + path
	case event.ToolEdit:
		if b.Name == "apply_patch" {
			// Codex hooks: tool_input.command holds the patch body
			body := str("command")
			if body == "" {
				body = str("input")
			}
			patch.Apply(ev, body, p.rel)
			break
		}
		p.edit(ev, b.Name, in)
	case event.ToolWeb:
		ev.Summary = strings.ToLower(b.Name)
	case event.ToolTodo, event.ToolTask:
		ev.Summary = strings.ToLower(b.Name)
		if ev.Tool == event.ToolTask {
			kind, _ := in["subagent_type"].(string)
			desc, _ := in["description"].(string)
			ev.Purpose = strings.ToLower(strings.TrimSpace(kind + "|" + strings.Join(strings.Fields(desc), " ")))
		}
	default:
		ev.Summary = b.Name
	}
	p.add(ev)
	return ev
}

func (p *Parser) setContent(path, content, h string) {
	p.files[path] = h
	if len(content) <= 2<<20 {
		p.contents[path] = content
	} else {
		delete(p.contents, path)
	}
}

type editSpec struct {
	Old, New string
	All      bool
}

func (p *Parser) edit(ev *event.Event, name string, in map[string]any) {
	path := ""
	if v, ok := in["file_path"].(string); ok {
		path = v
	} else if v, ok := in["notebook_path"].(string); ok {
		path = v
	}
	path = p.rel(path)
	ev.Paths = []string{path}
	ev.Summary = "edit " + path
	var specs []editSpec
	switch name {
	case "Edit":
		o, _ := in["old_string"].(string)
		n, _ := in["new_string"].(string)
		all, _ := in["replace_all"].(bool)
		specs = append(specs, editSpec{o, n, all})
	case "MultiEdit":
		if arr, ok := in["edits"].([]any); ok {
			for _, a := range arr {
				m, _ := a.(map[string]any)
				o, _ := m["old_string"].(string)
				n, _ := m["new_string"].(string)
				all, _ := m["replace_all"].(bool)
				specs = append(specs, editSpec{o, n, all})
			}
		}
	default:
		src, _ := in["new_source"].(string)
		specs = append(specs, editSpec{"", src, false})
	}
	var added, removed []string
	for _, s := range specs {
		a, r := lineDiff(lines(s.Old), lines(s.New))
		added = append(added, a...)
		removed = append(removed, r...)
		ev.Edits = append(ev.Edits, event.EditRef{Path: path, OldHash: hashStr(s.Old), NewHash: hashStr(s.New)})
	}
	ev.Patch = []event.PatchFile{{Path: path, Added: added, Removed: removed}}
	ev.AddedLines = map[string]int{path: len(added)}
	ev.RemovedLines = map[string]int{path: len(removed)}
	if cur, ok := p.contents[path]; ok && name != "NotebookEdit" {
		res, ok := applyEdits(cur, specs)
		if ok {
			h := hashStr(res)
			ev.WriteHashes = map[string]string{path: h}
			p.setContent(path, res, h)
			return
		}
	}
	// prior state unknown: hash of (path, old, new)
	var parts []string
	for _, e := range ev.Edits {
		parts = append(parts, e.OldHash, e.NewHash)
	}
	h := fp.Hash(append([]string{"edit", path}, parts...)...)
	ev.WriteHashes = map[string]string{path: "edit:" + h}
	p.files[path] = "edit:" + h
	delete(p.contents, path)
}

func applyEdits(cur string, specs []editSpec) (string, bool) {
	for _, s := range specs {
		if s.Old == "" {
			return "", false
		}
		if !strings.Contains(cur, s.Old) {
			return "", false
		}
		if s.All {
			cur = strings.ReplaceAll(cur, s.Old, s.New)
		} else {
			cur = strings.Replace(cur, s.Old, s.New, 1)
		}
	}
	return cur, true
}

func (p *Parser) user(r rawLine, ts time.Time, ref string) {
	if r.Message == nil {
		return
	}
	m := r.Message
	// a plain string prompt
	if m.Content.IsText {
		p.flush()
		p.prompt(m.Content.Text, ts, ref, r.IsMeta)
		return
	}
	blocks := m.Content.Blocks
	hadResult := false
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			hadResult = true
			// toolUseResult is decoded only for tools that need it; Read
			// results (file contents) are the bulk of transcripts
			var tur *toolUseResult
			if ev := p.byCallID[b.ToolUseID]; ev != nil && (ev.Tool == event.ToolShell || ev.RawTool == "Edit" || ev.RawTool == "Write") &&
				len(r.ToolUse) > 0 && r.ToolUse[0] == '{' {
				tur = &toolUseResult{}
				if json.Unmarshal(r.ToolUse, tur) != nil {
					tur = nil
				}
			}
			p.toolResult(b, tur)
		case "text":
			if !hadResult {
				p.flush()
				p.prompt(b.Text, ts, ref, r.IsMeta)
			}
		}
	}
}

func (p *Parser) prompt(text string, ts time.Time, ref string, meta bool) {
	t := strings.TrimSpace(text)
	if t == "" {
		return
	}
	ev := &event.Event{Kind: event.KindPrompt, TS: ts, SourceRef: ref, Summary: "prompt", Text: t}
	ev.Forced = rules.Current().ForcedPrompts.Match(t)
	p.add(ev)
	if p.Session.FirstPrompt == "" && !meta && !strings.HasPrefix(t, "<") && !strings.HasPrefix(t, "Caveat:") {
		p.Session.FirstPrompt = t
	}
}

// toolUseResult holds the toolUseResult fields the parser uses.
type toolUseResult struct {
	Stdout       *string `json:"stdout"`
	Stderr       string  `json:"stderr"`
	Type         string  `json:"type"`
	OriginalFile *string `json:"originalFile"`
	OldString    string  `json:"oldString"`
	NewString    string  `json:"newString"`
	ReplaceAll   bool    `json:"replaceAll"`
}

// maxScan bounds how much of a tool output is scanned for errors and failed
// tests; runners print failures and summaries at the end.
const maxScan = 64 << 10

func scanTail(s string) string {
	if len(s) > maxScan {
		return s[len(s)-maxScan:]
	}
	return s
}

func (p *Parser) toolResult(b block, tur *toolUseResult) {
	ev := p.byCallID[b.ToolUseID]
	if ev == nil {
		return
	}
	delete(p.byCallID, b.ToolUseID)
	p.Session.ToolUsesPaired++
	var text string
	if ev.Tool == event.ToolShell || b.IsError {
		text = contentText(b.Content)
	}
	out := text
	if tur != nil {
		if tur.Stdout != nil {
			out = *tur.Stdout
			if tur.Stderr != "" {
				out += "\n" + tur.Stderr
			}
		}
		// exact pre-edit content gives the true result hash when the
		// parser did not know the file before this edit
		if ev.Tool == event.ToolEdit && len(ev.Paths) == 1 && ev.RawTool == "Edit" && tur.OriginalFile != nil &&
			strings.HasPrefix(ev.WriteHashes[ev.Paths[0]], "edit:") {
			if res, ok := applyEdits(*tur.OriginalFile, []editSpec{{tur.OldString, tur.NewString, tur.ReplaceAll}}); ok {
				h := hashStr(res)
				ev.WriteHashes = map[string]string{ev.Paths[0]: h}
				p.setContent(ev.Paths[0], res, h)
			}
		}
		if ev.Tool == event.ToolWrite && len(ev.Paths) == 1 && tur.Type == "create" {
			ev.Created = []string{ev.Paths[0]}
		}
	}
	ev.IsError = b.IsError
	if ev.Tool == event.ToolShell {
		code := 0
		if b.IsError {
			code = 1
			if m := exitRe.FindStringSubmatch(text); m != nil {
				code, _ = strconv.Atoi(m[1])
			} else if strings.Contains(text, "<tool_use_error>") || strings.Contains(text, "was blocked") {
				code = -1 // rejected by the harness, never executed
			}
		}
		ev.ExitCode = &code
		if code != -1 {
			t := scanTail(out)
			ev.ErrFPs = fp.ErrorFPs(t)
			ev.FailedTests = testout.FailedTests(t)
		}
		ev.ResultFP = fp.ResultFP(ev.ExitCode, ev.ErrFPs, ev.FailedTests)
		ev.Text = tail(out, 4000)
	} else if b.IsError {
		code := 1
		ev.ExitCode = &code
		ev.ResultFP = fp.ResultFP(ev.ExitCode, nil, nil)
	}
}

func contentText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var bs []block
	if err := json.Unmarshal(raw, &bs); err == nil {
		var b strings.Builder
		for _, x := range bs {
			if x.Type == "text" {
				b.WriteString(x.Text)
				b.WriteByte('\n')
			}
		}
		return b.String()
	}
	return ""
}

// Finish flushes pending state and finalizes the session.
func (p *Parser) Finish() *event.Session {
	p.flush()
	if p.Session.ToolUses > 0 && p.Session.ToolUsesPaired < p.Session.ToolUses {
		// tool uses left unpaired at the very end (interrupted) are normal; the
		// format check uses the paired ratio.
	}
	return p.Session
}

// Files returns the current virtual workspace state.
func (p *Parser) Files() map[string]string { return p.files }

// ParseFile parses a transcript file, merging subagent transcripts found in
// <dir>/<session-id>/subagents/*.jsonl.
func ParseFile(path string) (*event.Session, error) {
	sess, err := parseOne(path, false)
	if err != nil {
		return nil, err
	}
	base := strings.TrimSuffix(path, ".jsonl")
	subs, _ := filepath.Glob(filepath.Join(base, "subagents", "*.jsonl"))
	sort.Strings(subs)
	for _, sp := range subs {
		ss, err := parseOne(sp, true)
		if err != nil || len(ss.Events) == 0 {
			continue
		}
		sess.Events = append(sess.Events, ss.Events...)
		sess.ToolUses += ss.ToolUses
		sess.ToolUsesPaired += ss.ToolUsesPaired
		sess.UsageLines += ss.UsageLines
		sess.UsageParsed += ss.UsageParsed
	}
	if len(subs) > 0 {
		sort.SliceStable(sess.Events, func(i, j int) bool { return sess.Events[i].TS.Before(sess.Events[j].TS) })
		for i, ev := range sess.Events {
			ev.Seq = int64(i)
			ev.SessionID = sess.ID
		}
	}
	return sess, nil
}

func parseOne(path string, sidechain bool) (*event.Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	p := NewParser(path)
	p.sidechain = sidechain
	h := sha256.New()
	rd := bufio.NewReaderSize(io.TeeReader(f, h), 1<<20)
	var off int64
	var buf []byte
	for {
		line, err := ReadLine(rd, &buf)
		if len(line) > 0 {
			p.Feed(line, off)
			off += int64(len(line))
		}
		if err != nil {
			break
		}
	}
	s := p.Finish()
	s.SourceHash = hex.EncodeToString(h.Sum(nil))
	if s.ID == "" {
		s.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	for _, ev := range s.Events {
		ev.SessionID = s.ID
	}
	return s, nil
}

// ReadLine returns the next line (including the newline) without allocating
// for lines that fit the reader's buffer; longer lines are assembled in buf.
// The returned slice is valid until the next call.
func ReadLine(rd *bufio.Reader, buf *[]byte) ([]byte, error) {
	line, err := rd.ReadSlice('\n')
	if err != bufio.ErrBufferFull {
		return line, err
	}
	*buf = append((*buf)[:0], line...)
	for {
		line, err = rd.ReadSlice('\n')
		*buf = append(*buf, line...)
		if err != bufio.ErrBufferFull {
			return *buf, err
		}
	}
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// lineDiff returns multiset differences: lines only in b (added) and only in a (removed).
func lineDiff(a, b []string) (added, removed []string) {
	cnt := map[string]int{}
	for _, x := range a {
		cnt[x]++
	}
	for _, x := range b {
		if cnt[x] > 0 {
			cnt[x]--
			continue
		}
		added = append(added, x)
	}
	cb := map[string]int{}
	for _, x := range b {
		cb[x]++
	}
	for _, x := range a {
		if cb[x] > 0 {
			cb[x]--
			continue
		}
		removed = append(removed, x)
	}
	return
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
