package reproach

import (
	"bufio"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"time"
)

// Message is one user message read from a host transcript, with the last
// thing the agent said before it.
type Message struct {
	Text string
	TS   time.Time
	// Before is the agent's last text before this message.
	Before string
	// Command is a slash command the user typed. It is not a message to
	// read, but the host reports it as a request, so it keeps the order.
	Command bool
	// Shell are the shell commands the agent ran after the previous user
	// message, in order, with whether each succeeded.
	Shell []Shell
}

// Shell is one shell command from a transcript.
type Shell struct {
	Cmd string
	OK  bool
}

// maxLine bounds one transcript line. Longer lines (large tool outputs)
// are skipped: they hold no user message.
const maxLine = 16 << 20

// ReadClaude reads the user messages of a Claude Code transcript. Tool
// results, meta entries, compaction summaries and harness notices are not
// user messages. The same message logged twice in a row (queued, then
// delivered) counts once.
func ReadClaude(r io.Reader) []Message {
	var out []Message
	var before, last string
	var shell []Shell
	pending := map[string]int{}
	scanLines(r, func(line []byte) {
		var d struct {
			Type      string          `json:"type"`
			Timestamp time.Time       `json:"timestamp"`
			IsMeta    bool            `json:"isMeta"`
			Compact   bool            `json:"isCompactSummary"`
			Message   json.RawMessage `json:"message"`
		}
		if json.Unmarshal(line, &d) != nil || len(d.Message) == 0 {
			return
		}
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(d.Message, &m) != nil {
			return
		}
		// other entry types (request copies the host keeps for its API
		// calls) repeat messages without a time
		if d.Type != "user" && d.Type != "assistant" {
			return
		}
		role := m.Role
		if role == "" {
			role = d.Type
		}
		blocks := contentBlocks(m.Content)
		switch role {
		case "assistant":
			for _, b := range blocks {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" && !hostNotice(b.Text) {
					before = b.Text
				}
				if b.Type == "tool_use" && b.Name == "Bash" {
					var in struct {
						Command string `json:"command"`
					}
					if json.Unmarshal(b.Input, &in) == nil && in.Command != "" {
						pending[b.ID] = len(shell)
						shell = append(shell, Shell{Cmd: in.Command})
					}
				}
			}
		case "user":
			if d.IsMeta || d.Compact {
				return
			}
			var parts []string
			for _, b := range blocks {
				if b.Type == "tool_result" {
					if i, ok := pending[b.ToolUseID]; ok && i < len(shell) {
						shell[i].OK = !b.IsError
						delete(pending, b.ToolUseID)
					}
					return
				}
				if b.Type == "text" {
					parts = append(parts, b.Text)
				}
			}
			t := strings.Join(parts, " ")
			if strings.HasPrefix(t, "<command-name>") || strings.HasPrefix(t, "<command-message>") {
				out = append(out, Message{TS: d.Timestamp, Command: true})
				last = ""
				return
			}
			if t == "" || strings.HasPrefix(t, "<") || strings.Contains(t, "Stop hook feedback") || strings.Contains(t, "[SYSTEM NOTIFICATION") {
				return
			}
			if t == last {
				return
			}
			last = t
			out = append(out, Message{Text: t, TS: d.Timestamp, Before: before, Shell: shell})
			shell = nil
			clear(pending)
		}
	})
	return out
}

// ReadCodex reads the user messages of a Codex rollout. Codex logs each
// typed message as a completed UserMessage item; the response items with
// the user role also carry instructions and hook feedback, which are not
// the user's words.
func ReadCodex(r io.Reader) []Message {
	var out []Message
	var before string
	scanLines(r, func(line []byte) {
		var d struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			Payload   struct {
				Type    string          `json:"type"`
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
				Item    struct {
					Type    string          `json:"type"`
					Content json.RawMessage `json:"content"`
				} `json:"item"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &d) != nil {
			return
		}
		p := d.Payload
		switch {
		case d.Type == "response_item" && p.Type == "message" && p.Role == "assistant":
			for _, b := range contentBlocks(p.Content) {
				if strings.TrimSpace(b.Text) != "" {
					before = b.Text
				}
			}
		case d.Type == "event_msg" && p.Type == "item_completed" && p.Item.Type == "UserMessage":
			var parts []string
			for _, b := range contentBlocks(p.Item.Content) {
				if b.Text != "" {
					parts = append(parts, b.Text)
				}
			}
			if t := strings.Join(parts, " "); t != "" {
				out = append(out, Message{Text: t, TS: d.Timestamp, Before: before})
			}
		}
	})
	return out
}

// hostNotice reports text the host writes into the agent's turn (a
// compaction warning), which is not the agent's answer.
func hostNotice(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "Autocompact is thrashing")
}

// CodexSessionID is the session id in a Codex rollout file name, which ends
// with the id.
func CodexSessionID(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if len(base) >= 36 {
		return base[len(base)-36:]
	}
	return base
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

func contentBlocks(raw json.RawMessage) []block {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []block{{Type: "text", Text: s}}
	}
	var bs []block
	_ = json.Unmarshal(raw, &bs)
	for i := range bs {
		if bs[i].Type == "input_text" || bs[i].Type == "output_text" {
			bs[i].Type = "text"
		}
	}
	return bs
}

func scanLines(r io.Reader, fn func([]byte)) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			// a line longer than the buffer: read it whole only if it is
			// within maxLine
			buf := append([]byte(nil), line...)
			for err == bufio.ErrBufferFull && len(buf) <= maxLine {
				line, err = br.ReadSlice('\n')
				buf = append(buf, line...)
			}
			for err == bufio.ErrBufferFull {
				_, err = br.ReadSlice('\n')
			}
			if len(buf) <= maxLine {
				fn(buf)
			}
		} else if len(line) > 0 {
			fn(line)
		}
		if err != nil {
			return
		}
	}
}
