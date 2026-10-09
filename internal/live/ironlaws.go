package live

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
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
func (s *Session) ironLawsCheck(ev *event.Event) {
	if s.queueRejected.Load() > 0 {
		return
	}
	bin := ironLawsBin(s.Cfg.IronLaws.Path)
	if bin == "" {
		return
	}
	for _, p := range ev.Paths {
		if pathnorm.IsAbs(p) || classify.IsTestPath(p) || detect.IsDocPath(p) {
			continue
		}
		abs := filepath.Join(s.Root, p)
		if _, err := os.Stat(abs); err != nil {
			continue
		}
		fs, err := RunIronLaws(bin, s.Root, abs, 60*time.Second)
		if err != nil || len(fs) == 0 {
			continue
		}
		lines := fileLines(abs)
		added := addedLines(ev, p)
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		if s.ironSeen == nil {
			s.ironSeen = map[string]bool{}
		}
		var sigs []detect.Signal
		for _, f := range fs {
			// A finding is error hiding this write added only when it sits on
			// a line the write added; one already in the file is not new, and
			// the same finding is reported once per session.
			text := ""
			if f.Line > 0 && f.Line <= len(lines) {
				text = strings.TrimSpace(lines[f.Line-1])
			}
			if added != nil && !added[text] {
				continue
			}
			key := p + "\x00" + f.RuleID + "\x00" + text
			if s.ironSeen[key] {
				continue
			}
			s.ironSeen[key] = true
			conf := 0.8
			if f.Confidence == "CONFIRMED" {
				conf = 0.9
			}
			sigs = append(sigs, detect.Signal{Detector: "S5", Rule: "s5.error_hiding", Confidence: conf, Level: detect.L1, Evidence: []int64{ev.Seq},
				Facts: map[string]any{"kind": "iron_laws:" + f.RuleID, "path": p, "line": f.Line, "source": "iron-laws"}})
		}
		s.deliver(s.eng.Emit(ev, sigs))
		s.mu.Unlock()
	}
}

// fileLines reads a file's lines; nil when it cannot be read.
func fileLines(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Split(string(b), "\n")
}

// addedLines is the set of trimmed lines a write added to path, nil when the
// write carries no patch for it (the added lines are unknown).
func addedLines(ev *event.Event, path string) map[string]bool {
	for _, pf := range ev.Patch {
		if pf.Path != path {
			continue
		}
		set := map[string]bool{}
		for _, l := range pf.Added {
			if t := strings.TrimSpace(l); t != "" {
				set[t] = true
			}
		}
		return set
	}
	return nil
}
