// Package outcome measures what an agent did after a verdict, from the
// ledger alone. The same measure applies to every experiment arm, so a
// withheld intervention and a sent one are compared on equal terms.
package outcome

import (
	"math"
	"slices"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
)

// Outcome is the behaviour in the window of tool calls after a verdict.
type Outcome struct {
	Calls      int  // tool calls observed in the window
	Complete   bool // the session ran the whole window
	Verified   bool // a check passed
	Repeated   bool // a command of the verdict's evidence ran again
	Recurred   bool // the same rule judged again
	TestEdited bool // a test file was written or edited
	Reverted   bool // changes were discarded with git
}

var revertRe = lazyre.New(`\bgit\s+(?:checkout\s+--\s|restore\s|reset\s+--hard\b)`)

// Measure reads the window of tool calls after the verdict at seq.
// evidence lists the event seqs the verdict cites; verdicts are the
// session's verdicts, used to see the rule judged again.
func Measure(seq int64, rule string, evidence []int64, evs []*event.Event, verdicts []detect.Signal, window int) Outcome {
	var cmds []string
	for _, ev := range evs {
		if ev.Tool == event.ToolShell && ev.CmdFP != "" && slices.Contains(evidence, ev.Seq) {
			cmds = append(cmds, ev.CmdFP)
		}
	}
	var o Outcome
	last := seq
	for _, ev := range evs {
		if ev.Seq <= seq || ev.Kind != event.KindTool {
			continue
		}
		if o.Calls == window {
			break
		}
		o.Calls++
		last = ev.Seq
		ok := ev.ExitCode != nil && *ev.ExitCode == 0
		switch ev.Tool {
		case event.ToolShell:
			if ev.Category == event.CatVerify && ok {
				o.Verified = true
			}
			if ev.CmdFP != "" && slices.Contains(cmds, ev.CmdFP) {
				o.Repeated = true
			}
			if revertRe.MatchString(ev.Summary) {
				o.Reverted = true
			}
		case event.ToolWrite, event.ToolEdit:
			if slices.ContainsFunc(ev.Paths, classify.IsTestPath) {
				o.TestEdited = true
			}
		}
	}
	o.Complete = o.Calls == window
	for _, v := range verdicts {
		if v.Rule == rule && v.Seq > seq && v.Seq <= last && v.Level > detect.L0 {
			o.Recurred = true
			break
		}
	}
	return o
}

// ConfSeq returns the mean of xs, values in [0, 1], and the half-width of an
// asymptotic confidence sequence: an interval that holds at every sample
// size at once, so the comparison may be read while data keep arriving
// (Waudby-Smith et al., "Time-uniform central limit theory", 2021). The
// width is tuned for about 100 samples.
func ConfSeq(xs []float64, alpha float64) (mean, half float64) {
	n := float64(len(xs))
	if n == 0 {
		return 0, math.Inf(1)
	}
	for _, x := range xs {
		mean += x
	}
	mean /= n
	var v float64
	for _, x := range xs {
		v += (x - mean) * (x - mean)
	}
	v /= n
	// a sample with no spread still has the spread a single change would add
	v = math.Max(v, 1/(4*n))
	l := -2 * math.Log(alpha)
	rho2 := (l + math.Log(l+1)) / 100
	k := n*v*rho2 + 1
	return mean, math.Sqrt(2 * k / (n * n * rho2) * math.Log(math.Sqrt(k)/alpha))
}
