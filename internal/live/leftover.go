package live

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/procscan"
)

// leftoverBudget bounds the process scan at a turn end.
const leftoverBudget = 200 * time.Millisecond

// shellWindows are the spans the session's shell calls ran in, with a
// little slack for process start. A background call runs until its
// completion, or until now while it has none. The caller holds s.mu.
func (s *Session) shellWindows(now time.Time) []procscan.Window {
	const slack = 2 * time.Second
	var out []procscan.Window
	ends := map[int64]time.Time{}
	for _, ev := range s.eng.St.Events {
		if ev.OriginSeq > 0 || strings.HasSuffix(ev.CallID, "#result") {
			ends[ev.OriginSeq] = ev.TS
		}
	}
	for _, ev := range s.eng.St.Events {
		if ev.Tool != event.ToolShell || ev.TS.IsZero() || ev.ExitCode != nil && *ev.ExitCode == -1 {
			continue
		}
		switch {
		case ev.Background:
			end, ok := ends[ev.Seq]
			if !ok {
				end = now
			}
			out = append(out, procscan.Window{From: ev.TS.Add(-slack), To: end.Add(slack)})
		case ev.OriginSeq > 0:
		default:
			from := ev.TS.Add(-time.Duration(ev.DurationMS)*time.Millisecond - slack)
			out = append(out, procscan.Window{From: from, To: ev.TS.Add(slack)})
		}
	}
	return out
}

// leftoverAtStop advises on processes the session started in its workspace
// that still run when a turn ends: a server left from a test run keeps a
// port and memory. It never ends them. A process set is told once. The
// caller holds s.mu.
func (s *Session) leftoverAtStop(ev *event.Event) {
	now := time.Now()
	procs := procscan.Leftover(s.Root, s.shellWindows(now), map[int]bool{os.Getpid(): true}, now.Add(leftoverBudget))
	if len(procs) == 0 {
		return
	}
	var keys, lines []string
	for _, p := range procs {
		keys = append(keys, fmt.Sprintf("%d@%d", p.PID, p.Started.Unix()))
		line := fmt.Sprintf("pid %d %s", p.PID, p.Cmd)
		if len(p.Ports) > 0 {
			var ps []string
			for _, port := range p.Ports {
				ps = append(ps, fmt.Sprint(port))
			}
			line += " (포트 " + strings.Join(ps, ", ") + ")"
		}
		line += fmt.Sprintf(", %s 경과", now.Sub(p.Started).Round(time.Minute))
		lines = append(lines, line)
	}
	sort.Strings(keys)
	key := strings.Join(keys, ",")
	if s.leftoverSeen == key {
		return
	}
	s.leftoverSeen = key
	sig := detect.Signal{Detector: "S8", Rule: "s8.leftover_process", Confidence: 0.7, Level: detect.L1, Evidence: []int64{ev.Seq}, Estimate: true,
		Facts: map[string]any{"count": len(procs), "procs": strings.Join(lines, "; "), "target": "leftover", "target_hash": key, "candidate_key": "leftover"}}
	s.deliver(s.eng.Emit(ev, []detect.Signal{sig}))
}
