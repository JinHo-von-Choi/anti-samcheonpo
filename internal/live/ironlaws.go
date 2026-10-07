package live

import (
	"encoding/json"
	"fmt"
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

// proposedContent returns the project-relative path and the full content a
// Write, Edit or MultiEdit would leave in the file. ok is false for anything
// it cannot reproduce exactly, so the caller never guesses.
func proposedContent(root string, in HookInput) (rel, content string, ok bool) {
	var p struct {
		FilePath   string `json:"file_path"`
		Content    string `json:"content"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
		Edits      []struct {
			OldString  string `json:"old_string"`
			NewString  string `json:"new_string"`
			ReplaceAll bool   `json:"replace_all"`
		} `json:"edits"`
	}
	if json.Unmarshal(in.ToolInput, &p) != nil || p.FilePath == "" {
		return "", "", false
	}
	abs := p.FilePath
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	r, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(r, "..") {
		return "", "", false
	}
	if in.ToolName == "Write" {
		return filepath.ToSlash(r), p.Content, true
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", "", false
	}
	cur := string(b)
	edits := p.Edits
	if in.ToolName == "Edit" {
		edits = append(edits[:0], struct {
			OldString  string `json:"old_string"`
			NewString  string `json:"new_string"`
			ReplaceAll bool   `json:"replace_all"`
		}{p.OldString, p.NewString, p.ReplaceAll})
	} else if in.ToolName != "MultiEdit" {
		return "", "", false
	}
	for _, e := range edits {
		if e.OldString == "" || !strings.Contains(cur, e.OldString) {
			return "", "", false
		}
		if e.ReplaceAll {
			cur = strings.ReplaceAll(cur, e.OldString, e.NewString)
		} else {
			cur = strings.Replace(cur, e.OldString, e.NewString, 1)
		}
	}
	return filepath.ToSlash(r), cur, true
}

// ironLawsPreBudget bounds the synchronous check inside the PreToolUse hook.
const ironLawsPreBudget = 3 * time.Second

// ironLawsPre runs before a write only to a file whose iron-laws error-hiding
// advice already reached the agent in this intent revision. It flags the
// write when the proposed content adds findings of an advised rule. Any
// error, missing tool or timeout lets the write through.
// category and seq are read from the event under s.mu by the caller; the
// event itself is shared with other hooks and is not read here.
func (s *Session) ironLawsPre(in HookInput, category event.Category, seq int64) *detect.Signal {
	if category != event.CatProduce || !s.Cfg.IronLaws.Enabled {
		return nil
	}
	rel, content, ok := proposedContent(s.Root, in)
	if !ok || classify.IsTestPath(rel) || detect.IsDocPath(rel) {
		return nil
	}
	s.mu.Lock()
	var revision uint64
	if s.intentRevision != nil {
		revision = s.intentRevision.Number
	}
	advised := map[string]bool{}
	if !s.released["s5.error_hiding"] {
		for key, on := range s.advised {
			parts := strings.Split(key, "\x00")
			if on && len(parts) == 4 && parts[0] == fmt.Sprint(revision) && parts[1] == "s5.error_hiding" && parts[3] == rel && strings.HasPrefix(parts[2], "iron_laws:") {
				advised[strings.TrimPrefix(parts[2], "iron_laws:")] = true
			}
		}
	}
	bin := ironLawsBin(s.Cfg.IronLaws.Path)
	s.mu.Unlock()
	if len(advised) == 0 || bin == "" {
		return nil
	}
	tmp, err := os.CreateTemp("", "samcheonpo-il-pre-*"+filepath.Ext(rel))
	if err != nil {
		return nil
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.WriteString(content)
	tmp.Close()
	if werr != nil {
		return nil
	}
	type result struct {
		fs  []IronLawsFinding
		err error
	}
	cur, next := make(chan result, 1), make(chan result, 1)
	go func() {
		abs := filepath.Join(s.Root, rel)
		if _, err := os.Stat(abs); err != nil {
			cur <- result{}
			return
		}
		fs, err := RunIronLaws(bin, s.Root, abs, ironLawsPreBudget)
		cur <- result{fs, err}
	}()
	go func() {
		fs, err := RunIronLaws(bin, s.Root, tmp.Name(), ironLawsPreBudget)
		next <- result{fs, err}
	}()
	before, after := <-cur, <-next
	if before.err != nil || after.err != nil {
		return nil
	}
	count := func(fs []IronLawsFinding) map[string]int {
		m := map[string]int{}
		for _, f := range fs {
			m[f.RuleID]++
		}
		return m
	}
	b, a := count(before.fs), count(after.fs)
	for id := range advised {
		if a[id] > b[id] {
			return &detect.Signal{Detector: "S5", Rule: "s5.error_hiding", Confidence: 0.85, Level: detect.L1, Evidence: []int64{seq},
				Facts: map[string]any{"kind": "iron_laws:" + id, "path": rel, "blocked": true, "source": "iron-laws"}}
		}
	}
	return nil
}
