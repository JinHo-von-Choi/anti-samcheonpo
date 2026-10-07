// Package bench implements ProgressBench scenarios: a compact step script that
// is rendered into a Claude Code transcript, analyzed, and checked against
// expectations.
package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
)

// Step is one scripted action.
type Step struct {
	Prompt  string  `yaml:"prompt,omitempty"`
	Bash    string  `yaml:"bash,omitempty"`
	Out     string  `yaml:"out,omitempty"`
	Exit    int     `yaml:"exit,omitempty"`
	Read    string  `yaml:"read,omitempty"`
	Grep    string  `yaml:"grep,omitempty"`
	Write   string  `yaml:"write,omitempty"`
	Content string  `yaml:"content,omitempty"`
	Create  bool    `yaml:"create,omitempty"`
	Edit    string  `yaml:"edit,omitempty"`
	Old     string  `yaml:"old,omitempty"`
	New     string  `yaml:"new,omitempty"`
	Msg     string  `yaml:"msg,omitempty"`
	Compact bool    `yaml:"compact,omitempty"`
	Seed    string  `yaml:"seed,omitempty"` // a file that existed before the session (no event)
	Repeat  int     `yaml:"repeat,omitempty"`
	Minutes float64 `yaml:"minutes,omitempty"` // time advance before the step
	Tokens  int64   `yaml:"tokens,omitempty"`  // output tokens for this step
}

// Expect describes the required outcome.
type Expect struct {
	Fires    []string `yaml:"fires"`     // rules that must produce a verdict at L1+ (or any level with suffix ":L0")
	NotFires []string `yaml:"not_fires"` // rules that must not produce any verdict (or none at level k and above with suffix ":Lk")
	MaxLevel *int     `yaml:"max_level"` // highest allowed primary level
	WasteMin int64    `yaml:"waste_min_krw"`
	WasteMax *int64   `yaml:"waste_max_krw"`
}

// Scenario is a bench task.
type Scenario struct {
	Name    string         `yaml:"name"`
	Symptom string         `yaml:"symptom"` // S1..S8 or "normal"
	Kind    string         `yaml:"kind"`    // positive | negative
	Task    string         `yaml:"task"`    // prompt used for live runs
	Model   string         `yaml:"model"`
	Steps   []Step         `yaml:"steps"`
	Expect  Expect         `yaml:"expect"`
	Config  map[string]any `yaml:"config"`
	Dir     string         `yaml:"-"`
}

// Load reads all scenarios under dir (one scenario.yml per subdirectory).
func Load(dir string) ([]Scenario, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "*", "scenario.yml"))
	sort.Strings(files)
	var out []Scenario
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var s Scenario
		dec := yaml.NewDecoder(strings.NewReader(string(b)))
		dec.KnownFields(true)
		if err := dec.Decode(&s); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		s.Dir = filepath.Dir(f)
		if s.Name == "" {
			s.Name = filepath.Base(s.Dir)
		}
		out = append(out, s)
	}
	return out, nil
}

// Render produces a Claude Code transcript (JSONL) for a scenario.
func Render(s Scenario, root string) []byte {
	model := s.Model
	if model == "" {
		model = "claude-sonnet-5-5"
	}
	sid := "bench-" + s.Name
	t := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	var b strings.Builder
	n := 0
	id := func(p string) string { n++; return fmt.Sprintf("%s_%04d", p, n) }
	emit := func(m map[string]any) {
		m["sessionId"] = sid
		m["cwd"] = root
		m["timestamp"] = t.Format(time.RFC3339Nano)
		j, _ := json.Marshal(m)
		b.Write(j)
		b.WriteByte('\n')
	}
	files := map[string]string{}
	usage := func(out int64) map[string]any {
		if out == 0 {
			out = 400
		}
		return map[string]any{"input_tokens": 20, "output_tokens": out, "cache_read_input_tokens": 30000, "cache_creation_input_tokens": 2000}
	}
	tool := func(name string, input map[string]any, resultText string, isErr bool, tur any, out int64) {
		tid := id("toolu")
		emit(map[string]any{"type": "assistant", "uuid": id("u"), "message": map[string]any{"id": id("msg"), "model": model, "role": "assistant",
			"content": []any{map[string]any{"type": "tool_use", "id": tid, "name": name, "input": input}}, "usage": usage(out)}})
		t = t.Add(15 * time.Second)
		res := map[string]any{"type": "tool_result", "tool_use_id": tid, "content": resultText}
		if isErr {
			res["is_error"] = true
		}
		line := map[string]any{"type": "user", "uuid": id("u"), "message": map[string]any{"role": "user", "content": []any{res}}}
		if tur != nil {
			line["toolUseResult"] = tur
		}
		emit(line)
		t = t.Add(15 * time.Second)
	}
	for _, st := range s.Steps {
		rep := st.Repeat
		if rep == 0 {
			rep = 1
		}
		for r := 0; r < rep; r++ {
			if st.Minutes > 0 {
				t = t.Add(time.Duration(st.Minutes * float64(time.Minute)))
			}
			switch {
			case st.Prompt != "":
				emit(map[string]any{"type": "user", "uuid": id("u"), "message": map[string]any{"role": "user", "content": st.Prompt}})
				t = t.Add(10 * time.Second)
			case st.Bash != "":
				text := st.Out
				isErr := st.Exit != 0
				if isErr {
					text = fmt.Sprintf("Exit code %d\n%s", st.Exit, st.Out)
				}
				tool("Bash", map[string]any{"command": st.Bash}, text, isErr, map[string]any{"stdout": st.Out, "stderr": "", "interrupted": false}, st.Tokens)
			case st.Read != "":
				tool("Read", map[string]any{"file_path": filepath.Join(root, st.Read)}, files[st.Read], false, nil, st.Tokens)
			case st.Grep != "":
				tool("Grep", map[string]any{"pattern": st.Grep, "path": root}, "", false, nil, st.Tokens)
			case st.Write != "":
				typ := "update"
				if _, ok := files[st.Write]; !ok || st.Create {
					typ = "create"
				}
				var orig any
				if o, ok := files[st.Write]; ok {
					orig = o
				}
				files[st.Write] = st.Content
				tool("Write", map[string]any{"file_path": filepath.Join(root, st.Write), "content": st.Content}, "File written", false,
					map[string]any{"type": typ, "filePath": filepath.Join(root, st.Write), "content": st.Content, "originalFile": orig}, st.Tokens)
			case st.Edit != "":
				orig := files[st.Edit]
				files[st.Edit] = strings.Replace(orig, st.Old, st.New, 1)
				tool("Edit", map[string]any{"file_path": filepath.Join(root, st.Edit), "old_string": st.Old, "new_string": st.New}, "The file has been updated",
					false, map[string]any{"filePath": filepath.Join(root, st.Edit), "oldString": st.Old, "newString": st.New, "originalFile": orig, "replaceAll": false}, st.Tokens)
			case st.Msg != "":
				emit(map[string]any{"type": "assistant", "uuid": id("u"), "message": map[string]any{"id": id("msg"), "model": model, "role": "assistant",
					"content": []any{map[string]any{"type": "text", "text": st.Msg}}, "usage": usage(st.Tokens)}})
				t = t.Add(10 * time.Second)
			case st.Seed != "":
				files[st.Seed] = st.Content
			case st.Compact:
				emit(map[string]any{"type": "system", "subtype": "compact_boundary", "uuid": id("u")})
			}
		}
	}
	return []byte(b.String())
}

// Outcome is a scenario result.
type Outcome struct {
	Name     string   `json:"name"`
	Symptom  string   `json:"symptom"`
	Kind     string   `json:"kind"`
	Pass     bool     `json:"pass"`
	Problems []string `json:"problems,omitempty"`
	Fired    []string `json:"fired"`
	MaxLevel int      `json:"max_level"`
	WasteKRW int64    `json:"waste_krw"`
	TotalKRW int64    `json:"total_krw"`
	Head     string   `json:"seal_head"`
}

// Run renders, analyzes and checks one scenario.
func Run(s Scenario, prices *cost.Table, work string) (Outcome, error) {
	o := Outcome{Name: s.Name, Symptom: s.Symptom, Kind: s.Kind}
	root := "/bench/" + s.Name
	p := filepath.Join(work, s.Name+".jsonl")
	if err := os.WriteFile(p, Render(s, root), 0o644); err != nil {
		return o, err
	}
	cfg := config.Default()
	if len(s.Config) > 0 {
		b, _ := yaml.Marshal(s.Config)
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return o, err
		}
	}
	if cfg.Detectors.Overrides == nil {
		cfg.Detectors.Overrides = map[string]int{}
	}
	sess, err := sources.Parse(sources.File{Path: p, Agent: "claude"})
	if err != nil {
		return o, err
	}
	r, err := analyze.Run(sess, analyze.Options{Config: cfg, ConfigHash: config.Hash(cfg), Prices: prices})
	if err != nil {
		return o, err
	}
	if err := analyze.CheckInvariant(sess, r.Totals); err != nil {
		return o, err
	}
	fired := map[string]detect.Level{}
	for _, v := range r.Verdicts {
		if l, ok := fired[v.Rule]; !ok || v.Level > l {
			fired[v.Rule] = v.Level
		}
		if v.Primary && int(v.Level) > o.MaxLevel {
			o.MaxLevel = int(v.Level)
		}
	}
	for k, l := range fired {
		o.Fired = append(o.Fired, fmt.Sprintf("%s:%s", k, l))
	}
	sort.Strings(o.Fired)
	o.WasteKRW = cost.Won(r.Totals.SymptomTotal())
	o.TotalKRW = cost.Won(r.Totals.Micro)
	for _, want := range s.Expect.Fires {
		rule, anyLevel := strings.CutSuffix(want, ":L0")
		l, ok := fired[rule]
		if !ok || (!anyLevel && l < detect.L1) {
			o.Problems = append(o.Problems, "울려야 하는데 울리지 않음: "+want)
		}
	}
	for _, no := range s.Expect.NotFires {
		// "rule:Lk" forbids level k and above; a bare rule forbids any level
		rule, min := no, detect.L0
		if i := strings.LastIndex(no, ":L"); i > 0 {
			if k, err := strconv.Atoi(no[i+2:]); err == nil {
				rule, min = no[:i], detect.Level(k)
			}
		}
		if l, ok := fired[rule]; ok && l >= min {
			o.Problems = append(o.Problems, "울리면 안 되는데 울림: "+no)
		}
	}
	if s.Expect.MaxLevel != nil && o.MaxLevel > *s.Expect.MaxLevel {
		o.Problems = append(o.Problems, fmt.Sprintf("개입 수위 L%d가 허용 L%d를 넘음", o.MaxLevel, *s.Expect.MaxLevel))
	}
	if s.Expect.WasteMin > 0 && o.WasteKRW < s.Expect.WasteMin {
		o.Problems = append(o.Problems, fmt.Sprintf("헛짓 %d원 < 기대 최소 %d원", o.WasteKRW, s.Expect.WasteMin))
	}
	if s.Expect.WasteMax != nil && o.WasteKRW > *s.Expect.WasteMax {
		o.Problems = append(o.Problems, fmt.Sprintf("헛짓 %d원 > 기대 최대 %d원", o.WasteKRW, *s.Expect.WasteMax))
	}
	o.Pass = len(o.Problems) == 0
	return o, nil
}
