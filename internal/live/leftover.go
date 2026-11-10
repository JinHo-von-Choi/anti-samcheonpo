package live

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/procscan"
)

// leftoverBudget bounds the process scan at a turn end.
const leftoverBudget = 200 * time.Millisecond

// shellWindows are the spans the session's shell calls ran in, with a
// little slack for process start. A background call counts once it has
// finished: until then its processes are the work it was started for. The
// caller holds s.mu.
// leftoverMinAge is how long a process has to have run before it counts as
// left behind.
var leftoverMinAge = 30 * time.Second

func (s *Session) shellWindows() []procscan.Window {
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
				continue
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
// port and memory. It never ends them. Each process is told once, and what
// the agent's own process still parents is not left behind. The caller
// holds s.mu.
func (s *Session) leftoverAtStop(ev *event.Event) {
	now := time.Now()
	found := procscan.Leftover(s.Root, s.shellWindows(), map[int]bool{os.Getpid(): true}, s.hostPID, now.Add(leftoverBudget))
	var procs []procscan.Proc
	var keys, lines []string
	var identities []map[string]any
	for _, p := range found {
		id := fmt.Sprintf("%d@%d", p.PID, p.Started.Unix())
		if s.leftoverSeen[id] {
			continue
		}
		if !p.Started.IsZero() && now.Sub(p.Started) < leftoverMinAge {
			continue // the host or a hook is still starting it
		}
		procs = append(procs, p)
		identities = append(identities, map[string]any{"pid": p.PID, "parent_pid": p.ParentPID, "executable_hash": fp.Hash(p.Exe), "command_summary_hash": fp.Hash(p.Cmd), "started_at": p.Started.UTC().Format(time.RFC3339Nano), "ports": p.Ports, "ownership": "workspace_time_candidate"})
		keys = append(keys, id)
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
	if len(procs) == 0 {
		return
	}
	sort.Strings(keys)
	key := strings.Join(keys, ",")
	sig := detect.Signal{Detector: "S8", Rule: "s8.leftover_process", Confidence: 0.7, Level: detect.L1, Evidence: []int64{ev.Seq}, Estimate: true,
		Facts: map[string]any{"count": len(procs), "procs": strings.Join(lines, "; "), "target": "leftover", "target_hash": key, "candidate_key": "leftover", "ownership": "workspace_time_candidate", "ownership_confirmed": false, "process_identities": identities, "cleanup_action": "manual_review"}}
	emitted := s.eng.Emit(ev, []detect.Signal{sig})
	// a report held back by the cooldown has told nobody, so these processes
	// stay new for the next turn end
	for _, v := range emitted {
		if v.Rule == sig.Rule && !v.Suppressed {
			if s.leftoverSeen == nil {
				s.leftoverSeen = map[string]bool{}
			}
			for _, id := range keys {
				s.leftoverSeen[id] = true
			}
		}
	}
	s.deliver(emitted)
}
