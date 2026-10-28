package live

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/claude"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// hookShape checks that a tool hook carries what the handlers read: the
// session, the tool name and call ID, the tool input as an object with the
// command or file the tool works on, and a result after the call. It returns
// why a payload does not match, "" when it does. Unknown tool names are not
// a mismatch: agents add tools, and an unknown tool is only observed.
func hookShape(name string, in HookInput) string {
	if in.Cursor != nil && in.Cursor.Shape != "" {
		return in.Cursor.Shape
	}
	if in.Copilot != nil && in.Copilot.Shape != "" {
		return in.Copilot.Shape
	}
	if in.Mcode != nil && in.Mcode.Shape != "" {
		return in.Mcode.Shape
	}
	switch name {
	case "PreToolUse", "PostToolUse", "PostToolUseFailure":
	default:
		return ""
	}
	if in.SessionID == "" {
		return name + ": session_id 없음"
	}
	if in.ToolName == "" {
		return name + ": tool_name 없음"
	}
	if in.ToolUseID == "" {
		return name + ": tool_use_id 없음"
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(in.ToolInput, &input); err != nil || input == nil {
		return fmt.Sprintf("%s(%s): tool_input이 객체가 아님", name, in.ToolName)
	}
	str := func(keys ...string) bool {
		for _, k := range keys {
			var s string
			if raw, ok := input[k]; ok && json.Unmarshal(raw, &s) == nil && s != "" {
				return true
			}
		}
		return false
	}
	switch {
	case in.ToolName == "apply_patch":
		if !str("command", "input", "patch") {
			return name + "(apply_patch): 패치 본문 없음"
		}
	case claude.NormalizeTool(in.ToolName) == event.ToolShell && in.ToolName != "BashOutput":
		if !str("command", "cmd") {
			return fmt.Sprintf("%s(%s): 셸 명령 없음", name, in.ToolName)
		}
	case in.ToolName == "Write" || in.ToolName == "Edit" || in.ToolName == "MultiEdit" || in.Cursor != nil && in.ToolName == "Delete":
		if !str("file_path", "path") {
			return fmt.Sprintf("%s(%s): 파일 경로 없음", name, in.ToolName)
		}
	}
	if name == "PostToolUse" && len(strings.TrimSpace(string(in.ToolResponse))) == 0 && in.Error == "" {
		return fmt.Sprintf("PostToolUse(%s): tool_response 없음", in.ToolName)
	}
	return ""
}

// capsFor requires d.mu. An agent gets its profile's capabilities once its
// version probe finished, whatever the version, unless a hook payload of
// that agent stopped matching what the handlers read.
func (d *Daemon) capsFor(agent string) adapter.Caps {
	if d.shapeBad[agent] != "" {
		return adapter.Observe(agent)
	}
	caps, _ := adapter.Resolve(agent, d.versions[agent])
	return caps
}

// markShapeBad drops every session of an agent to observation after one of
// its hook payloads did not match: a changed format could otherwise be
// misread into wrong advice or a wrong block. The downgrade is not
// permanent: when later payloads of the same hooks match again (see
// noteShapeGood), the agent is restored. The reason is shown on the status
// line while the downgrade lasts and by doctor.
func (d *Daemon) markShapeBad(agent, reason string) {
	d.mu.Lock()
	if d.shapeBad == nil {
		d.shapeBad = map[string]string{}
	}
	if d.shapeBad[agent] == "" {
		if d.shapeGood == nil {
			d.shapeGood = map[string]int{}
		}
		d.shapeGood[agent] = 0
	}
	d.shapeBad[agent] = reason
	var sessions []*Session
	for _, s := range d.sessions {
		if s.Agent == agent {
			sessions = append(sessions, s)
		}
	}
	d.mu.Unlock()
	observe := adapter.Observe(agent)
	for _, s := range sessions {
		s.mu.Lock()
		s.caps = observe
		s.shapeNote = reason
		s.mu.Unlock()
	}
}

// noteShapeGood counts consecutive well-shaped hooks of an agent and ends
// its observe-only downgrade once shapeRecoveryGood payloads in a row
// matched again: one bad payload can be one tool kind the handlers don't
// read (a benign format variance), so a run of good payloads is the
// evidence that the format is readable, not a single lucky one.
func (d *Daemon) noteShapeGood(agent, name string) {
	switch name {
	case "PreToolUse", "PostToolUse", "PostToolUseFailure":
	default:
		return
	}
	d.mu.Lock()
	if d.shapeBad[agent] == "" {
		d.mu.Unlock()
		return
	}
	if d.shapeGood == nil {
		d.shapeGood = map[string]int{}
	}
	d.shapeGood[agent]++
	if d.shapeGood[agent] < shapeRecoveryGood {
		d.mu.Unlock()
		return
	}
	reason := d.shapeBad[agent]
	delete(d.shapeBad, agent)
	delete(d.shapeGood, agent)
	var sessions []*Session
	for _, s := range d.sessions {
		if s.Agent == agent {
			sessions = append(sessions, s)
		}
	}
	// the same caps a fresh session would get at this moment
	caps := adapter.Observe(agent)
	ready, tracked := d.versionReady[agent]
	cause := observeCauseFor(caps, agent, ready, tracked)
	if ready || (!tracked && d.versions[agent] != "") {
		caps, _ = adapter.Resolve(agent, d.versions[agent])
		cause = observeCauseFor(caps, agent, ready, tracked)
	}
	d.mu.Unlock()
	for _, s := range sessions {
		s.mu.Lock()
		if s.shapeNote == reason {
			s.shapeNote = ""
		}
		s.observeCause = cause
		if !s.closed {
			s.caps = caps
		}
		s.mu.Unlock()
	}
}

// shapeRecoveryGood is the run of well-shaped hooks that ends an
// observe-only downgrade.
const shapeRecoveryGood = 3

// observeCauseFor names why capabilities are observe-only without a shape
// problem: a version probe that has not come back yet, or a versioned
// profile that is observe by itself (an untested version included). It
// reaches the ledger as the recovery delivery reason so a post-hoc
// analysis can distinguish a probe-pending race from a downgraded agent.
func observeCauseFor(caps adapter.Caps, agent string, probeReady, probeTracked bool) string {
	if caps != adapter.Observe(agent) {
		return ""
	}
	if !probeReady && probeTracked {
		return "probe_pending"
	}
	return "caps_observe"
}
