// Package codex parses Codex CLI rollout JSONL into common events.
package codex

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/claude"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/patch"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testout"
)

type rawLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type tokenUsage struct {
	In        int64 `json:"input_tokens"`
	Cached    int64 `json:"cached_input_tokens"`
	CacheW    int64 `json:"cache_write_input_tokens"`
	Out       int64 `json:"output_tokens"`
	Reasoning int64 `json:"reasoning_output_tokens"`
	Total     int64 `json:"total_tokens"`
}

// Parser builds a session from rollout lines. Two passes are needed because
// sessions recorded with item_completed events must not double count the
// response_item tool calls, so lines are buffered first.
type Parser struct {
	sess      *event.Session
	source    string
	model     string
	root      string
	itemMode  bool
	pending   []*event.Event // tool events since last token accounting
	lastTotal tokenUsage
	byCall    map[string]*event.Event
	contents  map[string]string
	nextSeq   int64
	compacts  int
	lastText  *event.Event
}

func hashStr(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16])
}

// ParseFile parses one rollout file in two streaming passes: the first only
// detects whether the session records item_completed events (so response_item
// tool calls are not double counted), the second parses.
func ParseFile(path string) (*event.Session, error) {
	itemMode, err := scanItemMode(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	rd := bufio.NewReaderSize(io.TeeReader(f, h), 1<<20)
	p := &Parser{
		sess:     &event.Session{Agent: "codex", Mode: "audit", SourcePath: path},
		source:   path,
		itemMode: itemMode,
		byCall:   map[string]*event.Event{},
		contents: map[string]string{},
	}
	var off int64
	var buf []byte
	for {
		line, err := claude.ReadLine(rd, &buf)
		if len(line) > 0 {
			if relevant(line, itemMode) {
				var r rawLine
				if json.Unmarshal(line, &r) == nil {
					p.feed(r, off)
				}
			}
			off += int64(len(line))
		}
		if err != nil {
			break
		}
	}
	p.sess.SourceHash = hex.EncodeToString(h.Sum(nil))
	if p.sess.ID == "" {
		p.sess.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	for _, ev := range p.sess.Events {
		ev.SessionID = p.sess.ID
	}
	return p.sess, nil
}

var (
	bItemCompleted = []byte(`"item_completed"`)
	bCmdExec       = []byte(`"CommandExecution"`)
	bFileChange    = []byte(`"FileChange"`)
	bResponseItem  = []byte(`"type":"response_item"`)
	bReasoning     = []byte(`"type":"reasoning"`)
	bItemReasoning = []byte(`"type":"Reasoning"`)
)

func scanItemMode(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	rd := bufio.NewReaderSize(f, 1<<20)
	var buf []byte
	for {
		line, err := claude.ReadLine(rd, &buf)
		if bytes.Contains(line, bItemCompleted) && (bytes.Contains(line, bCmdExec) || bytes.Contains(line, bFileChange)) {
			return true, nil
		}
		if err != nil {
			return false, nil
		}
	}
}

// relevant skips lines the parser ignores without decoding them.
func relevant(line []byte, itemMode bool) bool {
	head := line
	if len(head) > 200 {
		head = head[:200]
	}
	if itemMode && bytes.Contains(head, bResponseItem) {
		return false
	}
	if bytes.Contains(head, bReasoning) || bytes.Contains(head, bItemReasoning) {
		return false
	}
	return true
}

func (p *Parser) add(ev *event.Event) {
	ev.Seq = p.nextSeq
	ev.Basis = "hindsight"
	p.nextSeq++
	p.sess.Events = append(p.sess.Events, ev)
}

func (p *Parser) rel(path string) string {
	path = strings.TrimPrefix(path, "file://")
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	if p.root != "" && filepath.IsAbs(path) {
		if r, err := filepath.Rel(p.root, path); err == nil && !strings.HasPrefix(r, "..") {
			return filepath.ToSlash(r)
		}
	}
	return filepath.ToSlash(path)
}

func (p *Parser) feed(r rawLine, off int64) {
	ts, _ := time.Parse(time.RFC3339Nano, r.Timestamp)
	if !ts.IsZero() {
		if p.sess.StartedAt.IsZero() || ts.Before(p.sess.StartedAt) {
			p.sess.StartedAt = ts
		}
		if ts.After(p.sess.EndedAt) {
			p.sess.EndedAt = ts
		}
	}
	ref := fmt.Sprintf("%s#%d", p.source, off)
	var pl map[string]any
	_ = json.Unmarshal(r.Payload, &pl)
	ptype, _ := pl["type"].(string)
	switch r.Type {
	case "session_meta":
		if id, ok := pl["id"].(string); ok {
			p.sess.ID = id
		}
		if cwd, ok := pl["cwd"].(string); ok && p.root == "" {
			p.root = cwd
			p.sess.ProjectPath = cwd
		}
		if m, ok := pl["model"].(string); ok {
			p.model = m
		}
	case "turn_context":
		if cwd, ok := pl["cwd"].(string); ok && p.root == "" {
			p.root = cwd
			p.sess.ProjectPath = cwd
		}
		if m, ok := pl["model"].(string); ok && m != "" {
			p.model = m
			if p.sess.Model == "" {
				p.sess.Model = m
			}
		}
	case "compacted":
		if !p.itemMode {
			p.add(&event.Event{Kind: event.KindCompact, TS: ts, SourceRef: ref, Summary: "compact"})
		}
	case "event_msg":
		switch ptype {
		case "token_count":
			p.tokenCount(pl)
		case "item_completed":
			if p.itemMode {
				p.item(pl, ts, ref)
			}
		}
	case "response_item":
		if p.itemMode {
			return
		}
		p.responseItem(pl, ptype, ts, ref)
	}
}

func (p *Parser) tokenCount(pl map[string]any) {
	info, _ := pl["info"].(map[string]any)
	if rl, present := pl["rate_limits"]; present {
		b, _ := json.Marshal(rl)
		q := parseQuota(b)
		p.sess.QuotaKnown = q != nil
		p.sess.QuotaUsedPct, p.sess.QuotaWindowMin = 0, 0
		if q != nil {
			p.sess.QuotaUsedPct, p.sess.QuotaWindowMin = q.UsedPct, q.WindowMinutes
		}
	}
	if info == nil {
		return
	}
	b, _ := json.Marshal(info["total_token_usage"])
	var tot tokenUsage
	if json.Unmarshal(b, &tot) != nil {
		return
	}
	p.sess.UsageLines++
	p.sess.UsageParsed++
	if tot.Total <= p.lastTotal.Total {
		return // repeated record, no new usage
	}
	d := tokenUsage{
		In:     tot.In - p.lastTotal.In,
		Cached: tot.Cached - p.lastTotal.Cached,
		CacheW: tot.CacheW - p.lastTotal.CacheW,
		Out:    tot.Out - p.lastTotal.Out,
	}
	p.lastTotal = tot
	p.account(d, false)
}

// account distributes a usage delta over tool events since the last record.
func (p *Parser) account(d tokenUsage, final bool) {
	if final {
		return
	}
	u := event.Usage{In: max(0, d.In-d.Cached), CacheRead: d.Cached, CacheWrite: d.CacheW, Out: d.Out, Model: p.model}
	targets := p.pending
	p.pending = nil
	if len(targets) == 0 {
		ev := &event.Event{Kind: event.KindMessage, Summary: "message"}
		if p.lastText != nil {
			ev = p.lastText
			p.lastText = nil
		} else {
			if n := len(p.sess.Events); n > 0 {
				ev.TS = p.sess.Events[n-1].TS
			}
			p.add(ev)
		}
		targets = []*event.Event{ev}
	}
	n := int64(len(targets))
	for i, ev := range targets {
		x := event.Usage{In: u.In / n, Out: u.Out / n, CacheRead: u.CacheRead / n, CacheWrite: u.CacheWrite / n, Model: u.Model}
		if i == 0 {
			x.In += u.In % n
			x.Out += u.Out % n
			x.CacheRead += u.CacheRead % n
			x.CacheWrite += u.CacheWrite % n
		}
		ev.Usage.Add(x)
	}
}

func (p *Parser) textItem(it map[string]any, ts time.Time, ref string) {
	switch it["type"] {
	case "UserMessage":
		t := itemText(it["content"])
		if strings.TrimSpace(t) == "" {
			return
		}
		p.add(&event.Event{Kind: event.KindPrompt, TS: ts, SourceRef: ref, Summary: "prompt", Text: t})
		if p.sess.FirstPrompt == "" && !strings.HasPrefix(strings.TrimSpace(t), "<") {
			p.sess.FirstPrompt = t
		}
	case "AgentMessage":
		t := itemText(it["content"])
		if strings.TrimSpace(t) == "" {
			return
		}
		ev := &event.Event{Kind: event.KindMessage, TS: ts, SourceRef: ref, Summary: "message", Text: t, Usage: event.Usage{Model: p.model}}
		p.add(ev)
		p.lastText = ev
	}
}

func itemText(v any) string {
	arr, _ := v.([]any)
	var b strings.Builder
	for _, x := range arr {
		m, _ := x.(map[string]any)
		if t, ok := m["text"].(string); ok {
			b.WriteString(t)
		}
	}
	return b.String()
}

func (p *Parser) item(pl map[string]any, ts time.Time, ref string) {
	it, _ := pl["item"].(map[string]any)
	if it == nil {
		return
	}
	switch it["type"] {
	case "UserMessage", "AgentMessage":
		p.textItem(it, ts, ref)
	case "ContextCompaction":
		p.add(&event.Event{Kind: event.KindCompact, TS: ts, SourceRef: ref, Summary: "compact"})
	case "CommandExecution":
		cmd := commandString(it["command"])
		ev := p.shellEvent(cmd, ts, ref, "exec_command")
		if cwd, ok := it["cwd"].(string); ok {
			if d := p.rel(cwd); d != "." {
				ev.Dir = d
			}
		}
		out, _ := it["aggregated_output"].(string)
		if out == "" {
			so, _ := it["stdout"].(string)
			se, _ := it["stderr"].(string)
			out = so + "\n" + se
		}
		if ec, ok := it["exit_code"].(float64); ok {
			p.setResult(ev, int(ec), out)
		} else {
			p.setResult(ev, -1, out)
		}
		p.sess.ToolUses++
		p.sess.ToolUsesPaired++
	case "FileChange":
		ch, _ := it["changes"].(map[string]any)
		ev := &event.Event{Kind: event.KindTool, TS: ts, SourceRef: ref, RawTool: "apply_patch", Tool: event.ToolEdit,
			WriteHashes: map[string]string{}, AddedLines: map[string]int{}, RemovedLines: map[string]int{}}
		for path, v := range ch {
			c, _ := v.(map[string]any)
			rp := p.rel(path)
			ev.Paths = append(ev.Paths, rp)
			typ, _ := c["type"].(string)
			switch typ {
			case "add":
				content, _ := c["content"].(string)
				h := hashStr(content)
				ev.WriteHashes[rp] = h
				p.contents[rp] = content
				added := splitLines(content)
				ev.AddedLines[rp] = len(added)
				ev.Patch = append(ev.Patch, event.PatchFile{Path: rp, Added: added})
				ev.Created = append(ev.Created, rp)
				ev.Tool = event.ToolWrite
			case "delete":
				ev.Deleted = append(ev.Deleted, rp)
				ev.WriteHashes[rp] = "deleted"
				ev.Patch = append(ev.Patch, event.PatchFile{Path: rp, Deleted: true})
			default:
				diff, _ := c["unified_diff"].(string)
				added, removed := diffLines(diff)
				ev.AddedLines[rp] = len(added)
				ev.RemovedLines[rp] = len(removed)
				ev.Patch = append(ev.Patch, event.PatchFile{Path: rp, Added: added, Removed: removed})
				ev.WriteHashes[rp] = "patch:" + fp.Hash(rp, diff)
				ev.Edits = append(ev.Edits, event.EditRef{Path: rp, OldHash: hashStr(strings.Join(removed, "\n")), NewHash: hashStr(strings.Join(added, "\n"))})
			}
		}
		sortStrings(ev.Paths)
		ev.Summary = "edit " + strings.Join(ev.Paths, ",")
		p.add(ev)
		p.pending = append(p.pending, ev)
		p.sess.ToolUses++
		p.sess.ToolUsesPaired++
	case "McpToolCall":
		srv, _ := it["server"].(string)
		tool, _ := it["tool"].(string)
		ev := &event.Event{Kind: event.KindTool, TS: ts, SourceRef: ref, RawTool: "mcp__" + srv + "__" + tool, Tool: event.ToolOther, Summary: "mcp " + srv + "." + tool}
		if st, _ := it["status"].(string); st == "failed" {
			c := 1
			ev.ExitCode = &c
			ev.IsError = true
		}
		p.add(ev)
		p.pending = append(p.pending, ev)
		p.sess.ToolUses++
		p.sess.ToolUsesPaired++
	case "Extension":
		ev := &event.Event{Kind: event.KindTool, TS: ts, SourceRef: ref, RawTool: "web", Tool: event.ToolWeb, Summary: "web"}
		p.add(ev)
		p.pending = append(p.pending, ev)
	case "ImageView":
		path, _ := it["path"].(string)
		ev := &event.Event{Kind: event.KindTool, TS: ts, SourceRef: ref, RawTool: "view_image", Tool: event.ToolRead, Paths: []string{p.rel(path)}, Summary: "view image"}
		p.add(ev)
		p.pending = append(p.pending, ev)
	}
}

func commandString(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		parts := make([]string, 0, len(c))
		for _, x := range c {
			s, _ := x.(string)
			parts = append(parts, s)
		}
		// ["/bin/bash","-lc","<script>"]
		if len(parts) >= 3 && (strings.HasSuffix(parts[0], "sh") || strings.HasSuffix(parts[0], "bash")) && strings.HasPrefix(parts[1], "-") && strings.Contains(parts[1], "c") {
			return parts[len(parts)-1]
		}
		return strings.Join(parts, " ")
	}
	return ""
}

var readCmdRe = lazyre.New(`^(?:sed\s+-n\s+'[^']*'|cat|nl\s+-ba|head(?:\s+-n?\s*\d+)?|tail(?:\s+-n?\s*\d+)?)\s+(\S+)$`)

func (p *Parser) shellEvent(cmd string, ts time.Time, ref, raw string) *event.Event {
	ev := &event.Event{Kind: event.KindTool, TS: ts, SourceRef: ref, RawTool: raw, Tool: event.ToolShell, Cmd: cmd}
	ev.CmdNorm, ev.Dir = fp.NormalizeCmd(cmd)
	ev.CmdFP = fp.CmdFP(ev.CmdNorm)
	ev.ExecFP, ev.ExecCertain = fp.ExecFP(cmd, "")
	ev.Summary = "shell: " + trunc(ev.CmdNorm, 100)
	// Codex reads files through the shell; keep the path for re-read detection.
	if m := readCmdRe.FindStringSubmatch(ev.CmdNorm); m != nil {
		ev.Paths = []string{p.rel(strings.Trim(m[1], `'"`))}
	}
	p.add(ev)
	p.pending = append(p.pending, ev)
	return ev
}

func (p *Parser) setResult(ev *event.Event, code int, out string) {
	ev.ExitCode = &code
	ev.IsError = code != 0
	if code >= 0 {
		t := out
		if len(t) > 64<<10 {
			t = t[len(t)-64<<10:]
		}
		ev.ErrFPs = fp.ErrorFPs(t)
		ev.FailedTests = testout.FailedTests(t)
	}
	ev.ResultFP = fp.ResultFP(ev.ExitCode, ev.ErrFPs, ev.FailedTests)
	if len(out) > 4000 {
		out = out[len(out)-4000:]
	}
	ev.Text = out
}

var exitCodeRe = lazyre.New(`(?:Process exited with code|Exit code:?|"exit_code":)\s*(-?\d+)`)

func (p *Parser) responseItem(pl map[string]any, ptype string, ts time.Time, ref string) {
	switch ptype {
	case "function_call", "custom_tool_call":
		name, _ := pl["name"].(string)
		callID, _ := pl["call_id"].(string)
		var ev *event.Event
		switch name {
		case "shell", "exec_command", "local_shell", "container.exec":
			var args map[string]any
			if s, ok := pl["arguments"].(string); ok {
				_ = json.Unmarshal([]byte(s), &args)
			}
			cmd := commandString(args["command"])
			if cmd == "" {
				cmd, _ = args["cmd"].(string)
			}
			ev = p.shellEvent(cmd, ts, ref, name)
		case "apply_patch":
			input, _ := pl["input"].(string)
			if input == "" {
				var args map[string]any
				if s, ok := pl["arguments"].(string); ok {
					_ = json.Unmarshal([]byte(s), &args)
				}
				input, _ = args["input"].(string)
			}
			ev = p.patchEvent(input, ts, ref)
		default:
			ev = &event.Event{Kind: event.KindTool, TS: ts, SourceRef: ref, RawTool: name, Tool: event.ToolOther, Summary: name}
			p.add(ev)
			p.pending = append(p.pending, ev)
		}
		ev.CallID = callID
		p.byCall[callID] = ev
		p.sess.ToolUses++
	case "function_call_output", "custom_tool_call_output":
		callID, _ := pl["call_id"].(string)
		ev := p.byCall[callID]
		if ev == nil {
			return
		}
		delete(p.byCall, callID)
		p.sess.ToolUsesPaired++
		out := outputText(pl["output"])
		if ev.Tool == event.ToolShell {
			code := 0
			if m := exitCodeRe.FindStringSubmatch(out); m != nil {
				fmt.Sscan(m[1], &code)
			}
			p.setResult(ev, code, out)
		}
	case "message":
		role, _ := pl["role"].(string)
		t := itemText(pl["content"])
		if role == "user" && strings.TrimSpace(t) != "" && !strings.HasPrefix(strings.TrimSpace(t), "<") {
			p.add(&event.Event{Kind: event.KindPrompt, TS: ts, SourceRef: ref, Summary: "prompt", Text: t})
			if p.sess.FirstPrompt == "" {
				p.sess.FirstPrompt = t
			}
		} else if role == "assistant" && strings.TrimSpace(t) != "" {
			ev := &event.Event{Kind: event.KindMessage, TS: ts, SourceRef: ref, Summary: "message", Text: t, Usage: event.Usage{Model: p.model}}
			p.add(ev)
			p.lastText = ev
		}
	}
}

func outputText(v any) string {
	switch o := v.(type) {
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(o), &m) == nil {
			if s, ok := m["output"].(string); ok {
				if md, ok := m["metadata"].(map[string]any); ok {
					if ec, ok := md["exit_code"].(float64); ok {
						return fmt.Sprintf("%s\nExit code: %d", s, int(ec))
					}
				}
				return s
			}
		}
		return o
	case []any:
		return itemText(o)
	}
	return ""
}

func (p *Parser) patchEvent(body string, ts time.Time, ref string) *event.Event {
	ev := &event.Event{Kind: event.KindTool, TS: ts, SourceRef: ref, RawTool: "apply_patch"}
	patch.Apply(ev, body, p.rel)
	p.add(ev)
	p.pending = append(p.pending, ev)
	return ev
}

func diffLines(diff string) (added, removed []string) {
	for _, ln := range strings.Split(diff, "\n") {
		if strings.HasPrefix(ln, "+++") || strings.HasPrefix(ln, "---") {
			continue
		}
		if strings.HasPrefix(ln, "+") {
			added = append(added, ln[1:])
		} else if strings.HasPrefix(ln, "-") {
			removed = append(removed, ln[1:])
		}
	}
	return
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
