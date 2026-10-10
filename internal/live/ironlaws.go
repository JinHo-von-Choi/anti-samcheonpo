package live

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
)

// IronLawsFinding is one iron-laws violation (subset of its JSON report).
type IronLawsFinding struct {
	RuleID     string `json:"rule_id"`
	IronLaw    int    `json:"iron_law"`
	Severity   string `json:"severity"`
	FilePath   string `json:"file_path"`
	Line       int    `json:"line_number"`
	Confidence string `json:"confidence"`
	Message    string `json:"message"`
}

// ironLawsBin returns the iron-laws executable: the configured path or PATH.
func ironLawsBin(configured string) string {
	if configured != "" {
		if _, err := os.Stat(configured); err == nil {
			return configured
		}
		return ""
	}
	if p, err := exec.LookPath("iron-laws"); err == nil {
		return p
	}
	return ""
}

// RunIronLaws checks one file and returns its error-concealment findings
// (iron law 3), the family samcheonpo's S5 error-hiding rule covers.
func RunIronLaws(bin, root, file string, timeout time.Duration) ([]IronLawsFinding, error) {
	out, err := os.CreateTemp("", "samcheonpo-il-*.json")
	if err != nil {
		return nil, err
	}
	out.Close()
	defer os.Remove(out.Name())
	ctx, cancel := withTimeout(timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "audit", file, "--format", "json", "-o", out.Name())
	cmd.Dir = root
	_ = cmd.Run() // exit 1 means findings; the report decides
	b, err := os.ReadFile(out.Name())
	if err != nil || len(b) == 0 {
		return nil, err
	}
	var rep struct {
		Violations []IronLawsFinding `json:"violations"`
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		return nil, err
	}
	var res []IronLawsFinding
	for _, v := range rep.Violations {
		if v.IronLaw == 3 {
			res = append(res, v)
		}
	}
	return res, nil
}

// ironLawsCheck runs iron-laws on the code files a write changed and adds
// its findings to S5 error hiding.
func (s *Session) ironLawsCheck(ev *event.Event, snapshots map[string]string) {
	if s.queueRejected.Load() > 0 {
		return
	}
	bin := ironLawsBin(s.Cfg.IronLaws.Path)
	if bin == "" {
		return
	}
	for _, p := range ev.Paths {
		if snapshots[p] == "" {
			continue
		}
		if pathnorm.IsAbs(p) || classify.IsTestPath(p) || detect.IsDocPath(p) {
			continue
		}
		abs, safe := s.ironSourcePath(p)
		if !safe {
			continue
		}
		if _, err := os.Stat(abs); err != nil {
			continue
		}
		fs, err := RunIronLaws(bin, s.Root, abs, 60*time.Second)
		if err != nil || len(fs) == 0 {
			continue
		}
		data, readErr := readIronSource(abs)
		stable := readErr == nil && snapshots[p] != "" && snapshots[p] == fp.Hash(string(data))
		// Full writes carry the resulting content hash. Edits/diffs may carry
		// edit:/patch: markers, which cannot prove a whole-file version.
		if expected := ev.WriteHashes[p]; len(expected) == 32 && !strings.Contains(expected, ":") {
			stable = stable && expected == snapshots[p]
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		sigs := s.ironLawsSignals(ev, p, fs, strings.Split(string(data), "\n"), stable)
		s.deliver(s.eng.Emit(ev, sigs))
		s.mu.Unlock()
	}
}

// External audits use only bounded regular files. The snapshot is transient;
// only a fingerprint is retained until the asynchronous audit returns.
func readIronSource(file string) ([]byte, error) {
	st, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > 2<<20 {
		return nil, fmt.Errorf("source snapshot unavailable")
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if len(b) > 2<<20 {
		return nil, fmt.Errorf("source snapshot limit")
	}
	return b, err
}

func (s *Session) captureIronLaws(ev *event.Event) map[string]string {
	out := map[string]string{}
	for i, p := range ev.Paths {
		if i >= 8 {
			break
		}
		if pathnorm.IsAbs(p) || classify.IsTestPath(p) || detect.IsDocPath(p) {
			continue
		}
		abs, safe := s.ironSourcePath(p)
		if !safe {
			continue
		}
		if b, err := readIronSource(abs); err == nil {
			out[p] = fp.Hash(string(b))
		}
	}
	return out
}

// Reject escaping paths, including symlinked parent directories.
func (s *Session) ironSourcePath(p string) (string, bool) {
	root, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		return "", false
	}
	abs := filepath.Join(root, p)
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return abs, true
}

// ironLawsSignals is called with s.mu held. Unknown attribution remains L0:
// the file audit alone does not prove that this edit introduced the finding.
func (s *Session) ironLawsSignals(ev *event.Event, p string, fs []IronLawsFinding, lines []string, stable bool) []detect.Signal {
	added := addedLines(ev, p)
	if !stable || added == nil {
		reason := "changed_or_unobserved_source"
		if added == nil {
			reason = "missing_patch"
		}
		return []detect.Signal{{Detector: "S5", Rule: "s5.error_hiding", Level: detect.L0, Estimate: true, Evidence: []int64{ev.Seq}, Facts: map[string]any{"source": "iron-laws", "path": p, "attribution": "unknown", "reason": reason, "findings": len(fs)}}}
	}
	if s.ironSeen == nil {
		s.ironSeen = map[string]bool{}
	}
	var sigs []detect.Signal
	for _, f := range fs {
		if f.Line <= 0 || f.Line > len(lines) {
			continue
		}
		text := strings.TrimSpace(lines[f.Line-1])
		if text == "" || !added[text] {
			continue
		}
		key := fp.Hash("iron-laws-finding-v1", p, f.RuleID, text)
		if s.ironSeen[key] {
			continue
		}
		s.ironSeen[key] = true
		conf := 0.8
		if f.Confidence == "CONFIRMED" {
			conf = 0.9
		}
		sigs = append(sigs, detect.Signal{Detector: "S5", Rule: "s5.error_hiding", Confidence: conf, Level: detect.L1, Evidence: []int64{ev.Seq}, Facts: map[string]any{"kind": "iron_laws:" + f.RuleID, "path": p, "line": f.Line, "source": "iron-laws", "finding_key": key, "attribution": "changed_line"}})
	}
	for i := range sigs {
		sigs[i].Facts["finding_count"] = len(sigs)
	}
	return sigs
}

func (s *Session) restoreIronLaws(vs []detect.Signal) {
	if s.ironSeen == nil {
		s.ironSeen = map[string]bool{}
	}
	for _, v := range vs {
		if v.Rule == "s5.error_hiding" {
			if key, ok := v.Facts["finding_key"].(string); ok && key != "" {
				s.ironSeen[key] = true
			}
		}
	}
}

// addedLines is the set of trimmed lines a write added to path, nil when the
// write carries no patch for it (the added lines are unknown).
func addedLines(ev *event.Event, path string) map[string]bool {
	for _, pf := range ev.Patch {
		if pf.Path != path {
			continue
		}
		removed := map[string]bool{}
		for _, l := range pf.Removed {
			removed[strings.TrimSpace(l)] = true
		}
		set := map[string]bool{}
		for _, l := range pf.Added {
			if t := strings.TrimSpace(l); t != "" && !removed[t] {
				set[t] = true
			}
		}
		return set
	}
	return nil
}
