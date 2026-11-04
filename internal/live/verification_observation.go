package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
	"sort"
)

func (s *Session) observeVerification(ev *event.Event) {
	if ev.Category != event.CatVerify {
		return
	}
	cmd := ev.Cmd
	if cmd == "" {
		cmd = ev.CmdNorm
	}
	o := &verification.Observation{Version: "observed-check/1", Kind: "automated_check", State: "unknown", Source: "host_tool_result", CommandHash: fp.Hash(cmd), InputHash: ev.WSBefore}
	if r := ev.Reliability; r != nil {
		o.CheckID = r.CheckID
		o.EnvironmentHash = r.EnvironmentHash
		if r.InputHash != "" {
			o.InputHash = r.InputHash
		}
	}
	if detect.ExecutedVerify(ev) {
		o.State = "passed"
		if *ev.ExitCode != 0 || ev.IsError || len(ev.FailedTests) > 0 {
			o.State = "failed"
		}
	}
	if verification.EmptyTestRun(ev.Text) {
		o.State = "empty_run"
	}
	if ev.ExitCode != nil && *ev.ExitCode != 0 && fp.DiagnoseFailure(*ev.ExitCode, ev.Text).External() {
		o.State = "blocked_external"
	}
	if cmd == "" {
		o.CommandHash = ""
	}
	if ev.Background {
		o.State = "pending"
	}
	seen := map[string]bool{}
	for _, changed := range s.eng.St.Events {
		if changed.Seq >= ev.Seq || changed.Category != event.CatProduce || changed.IsError {
			continue
		}
		for _, p := range changed.Paths {
			if verification.CoversPathAt(cmd, p, s.Root, ev.Dir) {
				seen[p] = true
			}
		}
	}
	for p := range seen {
		o.RelatedPaths = append(o.RelatedPaths, p)
	}
	sort.Strings(o.RelatedPaths)
	ev.Verification = o
}
