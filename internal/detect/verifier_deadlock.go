package detect

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
)

// rejectMark is the latest run of a command outside the known checks that
// failed, with the number of runs in a row that failed the same way.
type rejectMark struct {
	result    string
	ws        string
	cmd       string
	exit      int
	count     int
	edited    bool // the workspace changed between failures with the same result
	seqs      []int64
	fired     bool
	waitFired bool
}

// trackRejection follows commands the classifier does not know (an external
// checker, a project script) that keep failing the same way. Known checks are
// S2's; failures outside the code are s2.environment's. When edits in between
// did not change the result, the edits cannot satisfy the tool and the
// decision goes to the user.
func (e *Engine) trackRejection(ev *event.Event, sigs *[]Signal) {
	if ev.Tool != event.ToolShell || !ev.Unknown || ev.ExitCode == nil || *ev.ExitCode == -1 || runID(ev) == "" {
		return
	}
	m := e.St.mar
	id := runID(ev)
	if *ev.ExitCode == 0 {
		delete(m.rejects, id)
		return
	}
	if c := fp.DiagnoseFailure(*ev.ExitCode, ev.Text).Class; fp.External(c) || c == fp.FailTransient {
		delete(m.rejects, id)
		return
	}
	r := m.rejects[id]
	if r == nil || r.result != ev.ResultFP {
		m.rejects[id] = &rejectMark{result: ev.ResultFP, ws: ev.WSBefore, cmd: ev.CmdNorm, exit: *ev.ExitCode, count: 1, seqs: []int64{ev.Seq}}
		return
	}
	r.count++
	r.seqs = append(r.seqs, ev.Seq)
	if ev.WSBefore != r.ws {
		r.edited = true
	}
	r.ws = ev.WSBefore
	need := e.threshold("s2.verifier_deadlock", e.Cfg.Detectors.S2.RejectionRepeats)
	if r.fired || !r.edited || need <= 0 || r.count < need {
		return
	}
	r.fired = true
	waste := e.markWaste(r.seqs[1:], "S2")
	e.add(sigs, Signal{Detector: "S2", Rule: "s2.verifier_deadlock", Confidence: 0.8, Level: L2, WasteMicro: waste, Evidence: append([]int64(nil), r.seqs...),
		Facts: map[string]any{"cmd": r.cmd, "count": r.count, "exit": r.exit, "kind": "repeat"}})
}

// rejectedRerun refuses running a repeatedly rejected command again with the
// workspace unchanged since its last failure: the same input gets the same
// rejection.
func (e *Engine) rejectedRerun(ev *event.Event, live bool) *Signal {
	if ev.Tool != event.ToolShell || !ev.Unknown || ev.WSBefore == "" || runID(ev) == "" || e.nondeterministic(ev.CmdNorm) {
		return nil
	}
	if live && !blockableRun(ev) {
		return nil
	}
	r := e.St.mar.rejects[runID(ev)]
	need := e.Cfg.Detectors.S2.RejectionRepeats
	if r == nil || need <= 0 || r.count < need || r.ws != ev.WSBefore {
		return nil
	}
	facts := map[string]any{"cmd": r.cmd, "count": r.count, "exit": r.exit, "kind": "unchanged_rerun"}
	if live {
		facts["blocked"] = true
	}
	return &Signal{Detector: "S2", Rule: "s2.verifier_deadlock", Confidence: 0.9, Level: L1, Evidence: append([]int64(nil), r.seqs...), Facts: facts}
}

// waitingRe matches a turn that ends by handing a choice to the user.
var waitingRe = lazyre.New(`(?i)(?:선택해\s*주|골라\s*주|결정해\s*주|정해\s*주|어떻게\s*(?:할까|진행할까|하시겠)|어느\s*(?:쪽|것|방법)|승인해\s*주|확인해\s*주시면|기다리겠|대기(?:하겠|합니다|하고)|which (?:option|one|approach)|please (?:choose|decide|confirm|advise)|waiting for your|let me know (?:how|which|whether|if you))`)

// rejectionWait raises s2.verifier_deadlock when a turn ends by asking the
// user to choose while a command keeps being rejected: the agent has stopped
// on the rejection, and only the user can relax the rule, approve the result
// or skip the check.
func (e *Engine) rejectionWait(msg *event.Event) *Signal {
	if msg == nil || !waitingRe.MatchString(msg.Text) {
		return nil
	}
	need := e.Cfg.Detectors.S2.RejectionRepeats
	if need <= 0 {
		return nil
	}
	// the most recent rejection is the one the turn stopped on
	var r *rejectMark
	for _, x := range e.St.mar.rejects {
		if x.count < need || x.waitFired || x.seqs[len(x.seqs)-1] <= e.St.LastProgress {
			continue
		}
		if r == nil || x.seqs[len(x.seqs)-1] > r.seqs[len(r.seqs)-1] {
			r = x
		}
	}
	if r == nil {
		return nil
	}
	r.waitFired = true
	return &Signal{Detector: "S2", Rule: "s2.verifier_deadlock", Confidence: 0.8, Level: L2, Evidence: append(append([]int64(nil), r.seqs...), msg.Seq),
		Facts: map[string]any{"cmd": r.cmd, "count": r.count, "exit": r.exit, "kind": "waiting"}}
}
