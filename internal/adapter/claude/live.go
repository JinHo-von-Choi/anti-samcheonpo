package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// HookToolUse creates a tool event from a PreToolUse/PostToolUse payload,
// reusing the transcript parsing rules.
func (p *Parser) HookToolUse(name, id string, input json.RawMessage, ts time.Time) *event.Event {
	ev := p.toolUse(block{Type: "tool_use", ID: id, Name: name, Input: input}, ts, "", false)
	ev.Basis = "live"
	p.Session.ToolUses++
	p.byCallID[id] = ev
	return ev
}

// HookToolResult completes a tool event from a PostToolUse (response) or
// PostToolUseFailure (errText) payload.
func (p *Parser) HookToolResult(id string, response json.RawMessage, isError bool, errText string) *event.Event {
	ev := p.byCallID[id]
	if ev == nil {
		return nil
	}
	var tur *toolUseResult
	if len(response) > 0 && response[0] == '{' {
		tur = &toolUseResult{}
		if json.Unmarshal(response, tur) != nil {
			tur = nil
		}
	}
	text := errText
	if text == "" {
		if tur != nil && tur.Stdout != nil {
			text = *tur.Stdout
		} else {
			var s string
			if json.Unmarshal(response, &s) == nil {
				text = s
			}
		}
	}
	content, _ := json.Marshal(text)
	p.toolResult(block{Type: "tool_result", ToolUseID: id, IsError: isError, Content: content}, tur)
	return ev
}

// UsageTracker reads a growing transcript and reports token usage per
// tool_use id (split evenly within each assistant message, remainder to the
// first call, like the retrospective parser).
type UsageTracker struct {
	Path     string
	Offset   int64
	msgs     map[string]*trackedMsg
	order    []string
	LastText string // last assistant text block (for Stop without payload text)
	Model    string
}

type trackedMsg struct {
	model    string
	usage    event.Usage
	toolIDs  []string
	reported map[string]event.Usage
	noTool   event.Usage // usage already reported for a tool-less message
}

// UsageUpdate is a usage delta for a tool use id ("" = message without tools).
type UsageUpdate struct {
	ToolUseID string
	MessageID string
	Delta     event.Usage
}

// NewUsageTracker creates a tracker.
func NewUsageTracker(path string) *UsageTracker {
	return &UsageTracker{Path: path, msgs: map[string]*trackedMsg{}}
}

// Poll reads new complete lines and returns usage deltas. A trailing partial
// line is left for the next poll.
func (u *UsageTracker) Poll() ([]UsageUpdate, error) {
	f, err := os.Open(u.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(u.Offset, io.SeekStart); err != nil {
		return nil, err
	}
	rd := bufio.NewReaderSize(f, 1<<20)
	var ups []UsageUpdate
	for {
		line, err := rd.ReadBytes('\n')
		if err != nil {
			// incomplete last line: do not advance past it
			break
		}
		u.Offset += int64(len(line))
		ups = append(ups, u.feed(line)...)
	}
	return ups, nil
}

func (u *UsageTracker) feed(line []byte) []UsageUpdate {
	if !bytes.Contains(line, []byte(`"assistant"`)) {
		return nil
	}
	var r rawLine
	if json.Unmarshal(line, &r) != nil || r.Type != "assistant" {
		return nil
	}
	if r.Message == nil || r.Message.ID == "" {
		return nil
	}
	m := r.Message
	tm := u.msgs[m.ID]
	if tm == nil {
		tm = &trackedMsg{model: m.Model, reported: map[string]event.Usage{}}
		u.msgs[m.ID] = tm
		u.order = append(u.order, m.ID)
		if len(u.order) > 500 {
			delete(u.msgs, u.order[0])
			u.order = u.order[1:]
		}
	}
	if m.Model != "" && m.Model != "<synthetic>" {
		tm.model = m.Model
		u.Model = m.Model
	}
	for _, b := range m.Content.Blocks {
		if b.Type == "tool_use" {
			tm.toolIDs = append(tm.toolIDs, b.ID)
		}
		if b.Type == "text" && b.Text != "" {
			u.LastText = b.Text
		}
	}
	if m.Usage != nil {
		tm.usage.In = max(tm.usage.In, m.Usage.In)
		tm.usage.Out = max(tm.usage.Out, m.Usage.Out)
		tm.usage.CacheRead = max(tm.usage.CacheRead, m.Usage.CacheRead)
		tm.usage.CacheWrite = max(tm.usage.CacheWrite, m.Usage.CacheWrite)
		tm.usage.CacheWrite1h = max(tm.usage.CacheWrite1h, m.Usage.oneHour())
	}
	return u.deltas(m.ID, tm)
}

func (u *UsageTracker) deltas(id string, tm *trackedMsg) []UsageUpdate {
	var out []UsageUpdate
	if len(tm.toolIDs) == 0 {
		d := sub(tm.usage, tm.noTool)
		if d.Total() > 0 {
			tm.noTool = tm.usage
			d.Model = tm.model
			out = append(out, UsageUpdate{MessageID: id, Delta: d})
		}
		return out
	}
	// usage reported to the message before its tool calls were known moves
	// from the message to the tools
	if tm.noTool.Total() != 0 {
		back := sub(event.Usage{}, tm.noTool)
		back.Model = tm.model
		out = append(out, UsageUpdate{MessageID: id, Delta: back})
		tm.noTool = event.Usage{}
	}
	n := int64(len(tm.toolIDs))
	for i, tid := range tm.toolIDs {
		share := event.Usage{In: tm.usage.In / n, Out: tm.usage.Out / n, CacheRead: tm.usage.CacheRead / n, CacheWrite: tm.usage.CacheWrite / n, CacheWrite1h: tm.usage.CacheWrite1h / n}
		if i == 0 {
			share.In += tm.usage.In % n
			share.Out += tm.usage.Out % n
			share.CacheRead += tm.usage.CacheRead % n
			share.CacheWrite += tm.usage.CacheWrite % n
			share.CacheWrite1h += tm.usage.CacheWrite1h % n
		}
		d := sub(share, tm.reported[tid])
		if d.Total() != 0 {
			tm.reported[tid] = add(tm.reported[tid], d)
			d.Model = tm.model
			out = append(out, UsageUpdate{ToolUseID: tid, MessageID: id, Delta: d})
		}
	}
	return out
}

func sub(a, b event.Usage) event.Usage {
	return event.Usage{In: a.In - b.In, Out: a.Out - b.Out, CacheRead: a.CacheRead - b.CacheRead, CacheWrite: a.CacheWrite - b.CacheWrite, CacheWrite1h: a.CacheWrite1h - b.CacheWrite1h}
}

func add(a, b event.Usage) event.Usage {
	return event.Usage{In: a.In + b.In, Out: a.Out + b.Out, CacheRead: a.CacheRead + b.CacheRead, CacheWrite: a.CacheWrite + b.CacheWrite, CacheWrite1h: a.CacheWrite1h + b.CacheWrite1h}
}

// SetRoot sets the project root for path normalization.
func (p *Parser) SetRoot(root string) {
	p.Root = root
	p.Session.ProjectPath = root
}

// AddEvent appends an externally built event (prompt, stop, compact) with a
// sequence number.
func (p *Parser) AddEvent(ev *event.Event) { p.add(ev) }
