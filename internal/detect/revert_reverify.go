package detect

import (
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
)

// revertState is what s1.revert_reverify remembers: when each workspace
// state was first seen, the last two checked states, and the loops found.
type revertState struct {
	firstSeen map[string]int64
	last      []checked // the latest checked states, oldest first (at most 2)
	loops     int
}

type checked struct {
	ws  string
	seq int64
	cmd string
}

// revertAskRe is a request that asks to see a test fail before the fix.
var revertAskRe = lazyre.New(`(?i)red[- ]?green|mutation|되돌려(?:서)?\s*(?:확인|실패)|실패하는지\s*(?:먼저\s*)?확인|실패를\s*확인|fail(?:s|ing)?\s+(?:first|before)`)

// revertReverify counts completed revert-and-restore loops: a check on
// state A, a check on an older state B (the change undone), then a check on
// A again (the change restored). Every such loop runs the tests three times
// for one change. The first loop is recorded, later ones are advised; the
// call is never stopped, because before the restore it cannot be known
// whether a check on an older state is part of a loop. git bisect and a
// request to see the test fail first are left alone.
func (e *Engine) revertReverify(ev *event.Event, sigs *[]Signal) {
	r := &e.St.revert
	if r.firstSeen == nil {
		r.firstSeen = map[string]int64{}
	}
	if ev.WSBefore != "" {
		if _, ok := r.firstSeen[ev.WSBefore]; !ok {
			r.firstSeen[ev.WSBefore] = ev.Seq
		}
	}
	if !ExecutedVerify(ev) || ev.WSBefore == "" {
		if ev.Tool == event.ToolShell && strings.Contains(ev.CmdNorm, "git bisect") {
			r.last = nil
		}
		return
	}
	cur := checked{ws: ev.WSBefore, seq: ev.Seq, cmd: ev.CmdNorm}
	if n := len(r.last); n > 0 && r.last[n-1].ws == cur.ws {
		r.last[n-1] = cur // the same state checked again is not a step
		return
	}
	if len(r.last) == 2 {
		a, b := r.last[0], r.last[1]
		if cur.ws == a.ws && r.firstSeen[b.ws] < r.firstSeen[a.ws] && !revertAskRe.MatchString(e.RequestText()) {
			r.loops++
			lvl, conf := L0, 0.6
			if r.loops >= 2 {
				lvl, conf = L1, 0.75
			}
			e.add(sigs, Signal{Detector: "S1", Rule: "s1.revert_reverify", Confidence: conf, Level: lvl, Evidence: []int64{a.seq, b.seq, cur.seq}, Estimate: true,
				Facts: map[string]any{"count": r.loops, "cmd": cur.cmd, "target": "revert_reverify", "target_hash": cur.ws}})
			r.last = []checked{cur}
			return
		}
	}
	r.last = append(r.last, cur)
	if len(r.last) > 2 {
		r.last = r.last[len(r.last)-2:]
	}
}
