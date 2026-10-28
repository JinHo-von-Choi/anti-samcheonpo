package detect

import (
	"path/filepath"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// RuleProductionGuard is a write to production data while the accepted
// contract names production and does not allow writing to it. It enforces
// a limit the user set, so it blocks like a protected path.
const RuleProductionGuard = "s3.production_guard"

// deployCheck judges a deploy (classify.Deploy) the way releases are judged,
// with one difference: the first deploy of a session also needs a check that
// passed after the last code change, because a deploy puts that code in
// front of users. The per-hour bound of releases is not applied: a rollback
// or a restart after an incident must stay possible.
func (e *Engine) deployCheck(ev *event.Event, live bool) *Signal {
	if ev.Tool != event.ToolShell {
		return nil
	}
	var commands, services []string
	if c := e.Contract; c != nil && c.Production != nil {
		commands, services = c.Production.DeployCommands, c.Production.Services
	}
	if !classify.Deploy(cmdOf(ev), commands, services) {
		return nil
	}
	m := e.St.mar
	facts := map[string]any{"cmd": ev.CmdNorm, "target": "deploy"}
	if live {
		facts["blocked"] = true
	}
	if ci := m.ci; ci != nil && !e.reproduced(ci) {
		facts["kind"], facts["ci_cmd"] = "ci_failed", ci.cmd
		return &Signal{Detector: "S5", Rule: "s5.deploy_without_preflight", Confidence: 0.9, Level: L1, Evidence: []int64{ci.seq}, Facts: facts}
	}
	if cmd, seq, ok := e.openUITiming(); ok {
		facts["kind"], facts["failing_cmd"] = "ui_timing", cmd
		return &Signal{Detector: "S5", Rule: "s5.deploy_without_preflight", Confidence: 0.8, Level: L1, Evidence: []int64{seq}, Facts: facts}
	}
	if w := e.St.lastCodeWriteSeq; e.St.codeWritten && !e.passedSince(w, "") {
		facts["kind"] = "unverified_change"
		return &Signal{Detector: "S5", Rule: "s5.deploy_without_preflight", Confidence: 0.85, Level: L1, Evidence: []int64{w}, Facts: facts}
	}
	return nil
}

// sameFile reports whether a sourced path names one of the files: the same
// path, or the same file name.
func sameFile(path string, files []string) (string, bool) {
	p := filepath.Clean(strings.Trim(path, `"'`))
	for _, f := range files {
		f = filepath.Clean(f)
		if p == f || filepath.Base(p) == filepath.Base(f) || strings.HasSuffix(p, "/"+f) {
			return f, true
		}
	}
	return "", false
}

// dataWriteCheck judges a database write before it runs. A write counts as
// production when the same command loads a production settings file and
// names no other target, or when it names a production host of the
// contract. A hand-written change to a migration history table is judged
// whether production or not, unless it names only this machine: the next
// deploy decides from that table which migrations to run or refuse.
func (e *Engine) dataWriteCheck(ev *event.Event) *Signal {
	if ev.Tool != event.ToolShell {
		return nil
	}
	info := classify.DataWrite(cmdOf(ev))
	if !info.Write && !info.MigrationHistory {
		return nil
	}
	var prod *struct{ kind, what string }
	if !info.Explicit {
		for _, f := range info.EnvFiles {
			if name, ok := sameFile(f, e.Contract.ProductionEnvFiles()); ok {
				prod = &struct{ kind, what string }{"env_file", name}
				break
			}
		}
	}
	if c := e.Contract; prod == nil && c != nil && c.Production != nil {
		for _, h := range info.Hosts {
			for _, p := range c.Production.Hosts {
				if strings.EqualFold(h, p) {
					prod = &struct{ kind, what string }{"host", p}
				}
			}
		}
	}
	facts := map[string]any{"cmd": ev.CmdNorm, "tool": info.Tool, "target": ev.CmdNorm}
	if prod != nil && info.Write {
		facts["kind"], facts["where"] = prod.kind, prod.what
		if c := e.Contract; c != nil && c.Production != nil {
			if c.Production.AllowWrite {
				return nil
			}
			facts["blocked"] = true
			return &Signal{Detector: "S3", Rule: RuleProductionGuard, Confidence: 1.0, Level: L3, Evidence: []int64{ev.Seq}, Facts: facts}
		}
		return &Signal{Detector: "S3", Rule: "s3.production_write", Confidence: 0.85, Level: L1, Evidence: []int64{ev.Seq}, Facts: facts}
	}
	if info.MigrationHistory && (prod != nil || !info.Local) {
		facts["kind"] = "migration_history"
		return &Signal{Detector: "S3", Rule: "s3.migration_history_write", Confidence: 0.9, Level: L1, Evidence: []int64{ev.Seq}, Facts: facts}
	}
	return nil
}
