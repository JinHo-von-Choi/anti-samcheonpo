// Package otel reads and writes agent sessions as OTLP/JSON traces using the
// OpenTelemetry GenAI semantic conventions (invoke_agent, chat, execute_tool
// spans; gen_ai.* attributes). It lets samcheonpo judge any agent that
// exports GenAI traces and lets other tools read samcheonpo sessions.
package otel

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

// OTLP/JSON shapes (subset).
type anyValue struct {
	StringValue *string  `json:"stringValue,omitempty"`
	IntValue    *string  `json:"intValue,omitempty"` // OTLP/JSON encodes int64 as string
	DoubleValue *float64 `json:"doubleValue,omitempty"`
	BoolValue   *bool    `json:"boolValue,omitempty"`
}

type keyValue struct {
	Key   string   `json:"key"`
	Value anyValue `json:"value"`
}

type span struct {
	TraceID           string     `json:"traceId"`
	SpanID            string     `json:"spanId"`
	ParentSpanID      string     `json:"parentSpanId,omitempty"`
	Name              string     `json:"name"`
	StartTimeUnixNano string     `json:"startTimeUnixNano"`
	EndTimeUnixNano   string     `json:"endTimeUnixNano"`
	Attributes        []keyValue `json:"attributes"`
	Status            *struct {
		Code int `json:"code"` // 2 = ERROR
	} `json:"status,omitempty"`
}

type scopeSpans struct {
	Spans []span `json:"spans"`
}

type resourceSpans struct {
	Resource struct {
		Attributes []keyValue `json:"attributes"`
	} `json:"resource"`
	ScopeSpans []scopeSpans `json:"scopeSpans"`
}

// Export is one OTLP/JSON trace export.
type Export struct {
	ResourceSpans []resourceSpans `json:"resourceSpans"`
}

func attrs(kv []keyValue) map[string]anyValue {
	m := map[string]anyValue{}
	for _, a := range kv {
		m[a.Key] = a.Value
	}
	return m
}

func str(m map[string]anyValue, k string) string {
	if v, ok := m[k]; ok && v.StringValue != nil {
		return *v.StringValue
	}
	return ""
}

func num(m map[string]anyValue, k string) int64 {
	v, ok := m[k]
	if !ok {
		return 0
	}
	switch {
	case v.IntValue != nil:
		n, _ := strconv.ParseInt(*v.IntValue, 10, 64)
		return n
	case v.DoubleValue != nil:
		return int64(*v.DoubleValue)
	case v.StringValue != nil:
		n, _ := strconv.ParseInt(*v.StringValue, 10, 64)
		return n
	}
	return 0
}

func nanos(s string) time.Time {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// tool name mapping to common names
func toolOf(name string) string {
	switch strings.ToLower(name) {
	case "bash", "shell", "exec_command", "run_command", "terminal":
		return event.ToolShell
	case "read", "read_file", "view":
		return event.ToolRead
	case "write", "write_file", "create_file":
		return event.ToolWrite
	case "edit", "multiedit", "apply_patch", "str_replace", "edit_file":
		return event.ToolEdit
	case "grep", "glob", "search", "find", "ls", "list":
		return event.ToolSearch
	case "webfetch", "websearch", "web_search", "fetch":
		return event.ToolWeb
	}
	return event.ToolOther
}

// ParseFile reads OTLP/JSON exports (one JSON document per line, as written by
// the collector file exporter, or a single document) into sessions grouped by
// gen_ai.conversation.id (falling back to the trace id).
func ParseFile(path string) ([]*event.Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	var all []struct {
		sp  span
		res map[string]anyValue
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		h.Write(line)
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var ex Export
		if err := json.Unmarshal(line, &ex); err != nil {
			return nil, fmt.Errorf("OTLP/JSON이 아니다: %w", err)
		}
		for _, rs := range ex.ResourceSpans {
			res := attrs(rs.Resource.Attributes)
			for _, ss := range rs.ScopeSpans {
				for _, sp := range ss.Spans {
					all = append(all, struct {
						sp  span
						res map[string]anyValue
					}{sp, res})
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(all, func(i, j int) bool {
		return nanos(all[i].sp.StartTimeUnixNano).Before(nanos(all[j].sp.StartTimeUnixNano))
	})
	sessions := map[string]*event.Session{}
	items := map[string][]*event.Event{} // ordered tool events and chat usage carriers
	var order []string
	for _, x := range all {
		a := attrs(x.sp.Attributes)
		sid := str(a, "gen_ai.conversation.id")
		if sid == "" {
			sid = x.sp.TraceID
		}
		s := sessions[sid]
		if s == nil {
			s = &event.Session{ID: sid, Agent: firstNonEmpty(str(a, "gen_ai.agent.name"), str(x.res, "service.name"), "otel"), Mode: "audit", SourcePath: path}
			s.ProjectPath = str(x.res, "process.working_directory")
			sessions[sid] = s
			order = append(order, sid)
		}
		ts := nanos(x.sp.StartTimeUnixNano)
		if !ts.IsZero() && (s.StartedAt.IsZero() || ts.Before(s.StartedAt)) {
			s.StartedAt = ts
		}
		if end := nanos(x.sp.EndTimeUnixNano); end.After(s.EndedAt) {
			s.EndedAt = end
		}
		ref := path + "#span:" + x.sp.SpanID
		op := str(a, "gen_ai.operation.name")
		switch {
		case op == "execute_tool" || strings.HasPrefix(x.sp.Name, "execute_tool"):
			name := str(a, "gen_ai.tool.name")
			ev := &event.Event{Kind: event.KindTool, TS: ts, RawTool: name, Tool: toolOf(name), CallID: str(a, "gen_ai.tool.call.id"), SourceRef: ref}
			args := map[string]any{}
			_ = json.Unmarshal([]byte(str(a, "gen_ai.tool.call.arguments")), &args)
			if ev.Tool == event.ToolShell {
				cmd, _ := args["command"].(string)
				ev.Cmd = cmd
				ev.CmdNorm, ev.Dir = fp.NormalizeCmd(cmd)
				ev.CmdFP = fp.CmdFP(ev.CmdNorm)
				ev.ExecFP, ev.ExecCertain = fp.ExecFP(cmd, "")
				ev.Summary = "shell: " + ev.CmdNorm
			} else {
				for _, k := range []string{"file_path", "path", "filePath"} {
					if p, ok := args[k].(string); ok && p != "" {
						ev.Paths = []string{p}
					}
				}
				ev.Summary = strings.ToLower(name)
			}
			s.ToolUses++
			s.ToolUsesPaired++
			code := 0
			if x.sp.Status != nil && x.sp.Status.Code == 2 {
				code = 1
			}
			if n, ok := a["samcheonpo.exit_code"]; ok && n.IntValue != nil {
				c, _ := strconv.Atoi(*n.IntValue)
				code = c
			}
			if ev.Tool == event.ToolShell {
				ev.ExitCode = &code
				ev.ResultFP = fp.ResultFP(ev.ExitCode, nil, nil)
			}
			items[sid] = append(items[sid], ev)
		case op == "chat" || op == "text_completion" || op == "generate_content":
			u := event.Usage{In: num(a, "gen_ai.usage.input_tokens"), Out: num(a, "gen_ai.usage.output_tokens"),
				CacheRead: num(a, "gen_ai.usage.cache_read.input_tokens"), CacheWrite: num(a, "gen_ai.usage.cache_creation.input_tokens"),
				CacheWrite1h: num(a, "samcheonpo.usage.cache_creation_1h.input_tokens"),
				Model:        firstNonEmpty(str(a, "gen_ai.response.model"), str(a, "gen_ai.request.model"))}
			// cached tokens are part of the input count in the GenAI convention
			u.In = max(0, u.In-u.CacheRead-u.CacheWrite)
			s.UsageLines++
			s.UsageParsed++
			if s.Model == "" {
				s.Model = u.Model
			}
			items[sid] = append(items[sid], &event.Event{Kind: event.KindMessage, TS: ts, Summary: "message", Usage: u, SourceRef: ref})
		case op == "invoke_agent":
			if p := str(a, "gen_ai.prompt"); p != "" && s.FirstPrompt == "" {
				s.FirstPrompt = p
			}
		}
	}
	sum := hex.EncodeToString(h.Sum(nil))
	var out []*event.Session
	for _, id := range order {
		s := sessions[id]
		s.SourceHash = sum
		list := items[id]
		// each chat span's usage belongs to the tool calls up to the next chat
		for i := 0; i < len(list); i++ {
			ev := list[i]
			if ev.Kind != event.KindMessage {
				continue
			}
			var targets []*event.Event
			for k := i + 1; k < len(list) && list[k].Kind == event.KindTool; k++ {
				targets = append(targets, list[k])
			}
			if len(targets) == 0 {
				continue
			}
			u := ev.Usage
			ev.Usage = event.Usage{}
			n := int64(len(targets))
			for k, t := range targets {
				x := event.Usage{In: u.In / n, Out: u.Out / n, CacheRead: u.CacheRead / n, CacheWrite: u.CacheWrite / n, CacheWrite1h: u.CacheWrite1h / n, Model: u.Model}
				if k == 0 {
					x.In += u.In % n
					x.Out += u.Out % n
					x.CacheRead += u.CacheRead % n
					x.CacheWrite += u.CacheWrite % n
					x.CacheWrite1h += u.CacheWrite1h % n
				}
				t.Usage.Add(x)
			}
		}
		for _, ev := range list {
			if ev.Kind == event.KindMessage && ev.Usage.Total() == 0 {
				continue
			}
			ev.Seq = int64(len(s.Events))
			ev.SessionID = id
			ev.Basis = "hindsight"
			s.Events = append(s.Events, ev)
		}
		out = append(out, s)
	}
	return out, nil
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func sv(s string) anyValue             { return anyValue{StringValue: &s} }
func iv(n int64) anyValue              { s := strconv.FormatInt(n, 10); return anyValue{IntValue: &s} }
func kv(k string, v anyValue) keyValue { return keyValue{Key: k, Value: v} }

// Encode writes a session as one OTLP/JSON export: an invoke_agent span with
// a chat span per event usage and an execute_tool span per tool event.
func Encode(s *event.Session) ([]byte, error) {
	trace := fp.Hash("trace", s.ID)
	root := fp.Hash("span", s.ID)[:16]
	t := func(x time.Time) string {
		if x.IsZero() {
			return "0"
		}
		return strconv.FormatInt(x.UnixNano(), 10)
	}
	spans := []span{{TraceID: trace, SpanID: root, Name: "invoke_agent " + s.Agent, StartTimeUnixNano: t(s.StartedAt), EndTimeUnixNano: t(s.EndedAt),
		Attributes: []keyValue{kv("gen_ai.operation.name", sv("invoke_agent")), kv("gen_ai.conversation.id", sv(s.ID)), kv("gen_ai.agent.name", sv(s.Agent))}}}
	for _, ev := range s.Events {
		id := fp.Hash("span", s.ID, strconv.FormatInt(ev.Seq, 10))[:16]
		u := ev.Usage
		if u.Total() > 0 {
			spans = append(spans, span{TraceID: trace, SpanID: id + "c", ParentSpanID: root, Name: "chat " + u.Model, StartTimeUnixNano: t(ev.TS.Add(-time.Nanosecond)), EndTimeUnixNano: t(ev.TS),
				Attributes: []keyValue{kv("gen_ai.operation.name", sv("chat")), kv("gen_ai.conversation.id", sv(s.ID)), kv("gen_ai.request.model", sv(u.Model)),
					kv("gen_ai.usage.input_tokens", iv(u.In+u.CacheRead+u.CacheWrite)), kv("gen_ai.usage.output_tokens", iv(u.Out)),
					kv("gen_ai.usage.cache_read.input_tokens", iv(u.CacheRead)), kv("gen_ai.usage.cache_creation.input_tokens", iv(u.CacheWrite)),
					kv("samcheonpo.usage.cache_creation_1h.input_tokens", iv(u.CacheWrite1h))}})
		}
		if ev.Kind != event.KindTool {
			continue
		}
		args := map[string]any{}
		if ev.Tool == event.ToolShell {
			args["command"] = ev.CmdNorm
		} else if len(ev.Paths) > 0 {
			args["file_path"] = ev.Paths[0]
		}
		ab, _ := json.Marshal(args)
		name := ev.RawTool
		if name == "" {
			name = ev.Tool
		}
		sp := span{TraceID: trace, SpanID: id, ParentSpanID: root, Name: "execute_tool " + name, StartTimeUnixNano: t(ev.TS), EndTimeUnixNano: t(ev.TS),
			Attributes: []keyValue{kv("gen_ai.operation.name", sv("execute_tool")), kv("gen_ai.conversation.id", sv(s.ID)), kv("gen_ai.tool.name", sv(name)),
				kv("gen_ai.tool.call.id", sv(ev.CallID)), kv("gen_ai.tool.call.arguments", sv(string(ab)))}}
		if ev.ExitCode != nil {
			sp.Attributes = append(sp.Attributes, kv("samcheonpo.exit_code", iv(int64(*ev.ExitCode))))
			if *ev.ExitCode > 0 {
				sp.Status = &struct {
					Code int `json:"code"`
				}{2}
			}
		}
		spans = append(spans, sp)
	}
	ex := Export{ResourceSpans: []resourceSpans{{ScopeSpans: []scopeSpans{{Spans: spans}}}}}
	ex.ResourceSpans[0].Resource.Attributes = []keyValue{kv("service.name", sv(s.Agent)), kv("process.working_directory", sv(s.ProjectPath))}
	return json.Marshal(ex)
}
