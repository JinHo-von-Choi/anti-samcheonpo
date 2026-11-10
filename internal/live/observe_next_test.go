package live

import (
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestDesiredNextIsTheBehaviorTheAdviceAskedFor(t *testing.T) {
	shell := func(seq int64, cmd string) *event.Event {
		return &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Seq: seq, Cmd: cmd}
	}
	edit := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Seq: 3, Paths: []string{"a.go"}}
	answer := &event.Event{Kind: event.KindMessage, Seq: 4}
	verify := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Seq: 3, Category: event.CatVerify, Cmd: "go test ./..."}
	cases := []struct {
		name, rule string
		events     []*event.Event
		want       bool
	}{
		{"a code change after a repeated check", "s1.identical_rerun", []*event.Event{edit}, true},
		{"an answer with no further check", "s1.identical_rerun", []*event.Event{answer}, true},
		{"checking again before the answer", "s1.identical_rerun", []*event.Event{verify, answer}, false},
		{"cleaning up a process", "s8.leftover_process", []*event.Event{shell(3, "pkill -f vite ")}, true},
		{"a name that only contains kill", "s8.leftover_process", []*event.Event{shell(3, "python skill.py ")}, false},
		{"before the advice", "s1.identical_rerun", []*event.Event{{Kind: event.KindMessage, Seq: 1}}, false},
		{"a rule with no defined behavior", "s3.out_of_scope", []*event.Event{edit, answer}, false},
	}
	for _, c := range cases {
		if got := desiredNext(c.rule, c.events, 2, 12); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}
