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
		if strings.HasPrefix(p, "/") || classify.IsTestPath(p) || detect.IsDocPath(p) {
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
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		var sigs []detect.Signal
		for _, f := range fs {
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
