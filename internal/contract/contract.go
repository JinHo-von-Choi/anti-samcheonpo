// Package contract implements Progress Contract v1: the task contract schema,
// validation, scope matching, acceptance state and check candidates.
package contract

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sockpath"
)

// SpecVersion is the Progress Contract version this package implements.
const SpecVersion = "progress-contract/1"
const SpecVersion2 = "progress-contract/2"

// ReuseScope is an explicit assertion accepted with the check: all local
// inputs and runtime files are listed, with no unobserved external service.
// Omission preserves v1 behavior (always execute). Paths are literal, not globs.
type ReuseScope struct {
	Inputs           []string `yaml:"inputs" json:"inputs"`
	EnvironmentFiles []string `yaml:"environment_files" json:"environment_files"`
	Deterministic    bool     `yaml:"deterministic" json:"deterministic"`
	MaxAgeSec        int      `yaml:"max_age_sec" json:"max_age_sec"`
}

// Check is one completion criterion.
type Check struct {
	Reuse      *ReuseScope `yaml:"reuse,omitempty" json:"reuse,omitempty"`
	ID         string      `yaml:"id,omitempty" json:"id"`
	Check      string      `yaml:"check,omitempty" json:"check,omitempty"`         // machine-verifiable shell command
	Expect     string      `yaml:"expect,omitempty" json:"expect,omitempty"`       // exit0 (default)
	Manual     string      `yaml:"manual,omitempty" json:"manual,omitempty"`       // human-confirmed criterion
	Isolation  string      `yaml:"isolation,omitempty" json:"isolation,omitempty"` // "" | worktree
	Pure       bool        `yaml:"pure,omitempty" json:"pure,omitempty"`
	TimeoutSec int         `yaml:"timeout_sec,omitempty" json:"timeout_sec,omitempty"`
}

// Contract is .samcheonpo/contract.yml.
type Contract struct {
	Spec  string  `yaml:"spec,omitempty" json:"spec,omitempty"`
	Goal  string  `yaml:"goal" json:"goal"`
	Done  []Check `yaml:"done" json:"done"`
	Scope struct {
		Allow   []string `yaml:"allow" json:"allow"`
		Protect []string `yaml:"protect" json:"protect"`
	} `yaml:"scope" json:"scope"`
	Budget struct {
		KRW              int64 `yaml:"krw" json:"krw"`
		Minutes          int   `yaml:"minutes" json:"minutes"`
		SameErrorRetries int   `yaml:"same_error_retries" json:"same_error_retries"`
	} `yaml:"budget" json:"budget"`
	Forbid []string `yaml:"forbid" json:"forbid"`
	// Explore marks the contract as a research task (S4 is not raised).
	Explore bool `yaml:"explore,omitempty" json:"explore,omitempty"`
}

// State is the acceptance state (stored in .samcheonpo/contract.state.json).
type State string

const (
	StateDraft    State = "draft"
	StateAccepted State = "accepted"
	StateStale    State = "stale"   // file changed after acceptance
	StateSkipped  State = "skipped" // user chose to proceed without a contract
	StateDone     State = "done"
)

// Acceptance records what the user accepted.
type Acceptance struct {
	RequestedGoal    string `json:"requested_goal,omitempty"`
	PreviousFileHash string `json:"previous_file_hash,omitempty"`
	State            State  `json:"state"`
	ChecksHash       string `json:"checks_hash"`
	// AuthorityHash binds every accepted field (goal, checks, scope,
	// protection, budget, forbidden actions); any change needs re-acceptance.
	AuthorityHash string    `json:"authority_hash,omitempty"`
	FileHash      string    `json:"file_hash"`
	AcceptedAt    time.Time `json:"accepted_at"`
	// SideEffect lists check ids that changed the workspace and are excluded from automatic runs.
	SideEffect []string `json:"side_effect,omitempty"`
	// MAC is keyed by a secret in the user's samcheonpo home, so a state file
	// written by an agent or copied from another project grants nothing.
	MAC string `json:"mac,omitempty"`
}

// Dir returns the contract directory of a project.
func Dir(project string) string { return filepath.Join(project, ".samcheonpo") }

// Path returns the contract file path of a project.
func Path(project string) string { return filepath.Join(Dir(project), "contract.yml") }

// ValidationError is a schema error with a field path.
type ValidationError struct {
	Field string
	Msg   string
}

func (e ValidationError) Error() string { return e.Field + ": " + e.Msg }

// Parse parses and validates contract YAML.
func Parse(b []byte) (*Contract, []ValidationError) {
	var c Contract
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, []ValidationError{{Field: "(file)", Msg: err.Error()}}
	}
	var errs []ValidationError
	if c.Spec != "" && c.Spec != SpecVersion && c.Spec != SpecVersion2 {
		errs = append(errs, ValidationError{"spec", "지원하지 않는 계약 버전이다"})
	}
	if strings.TrimSpace(c.Goal) == "" {
		errs = append(errs, ValidationError{"goal", "목표 한 문장이 필요하다"})
	}
	if len(c.Done) == 0 {
		errs = append(errs, ValidationError{"done", "완료 조건이 하나 이상 필요하다"})
	}
	ids := map[string]bool{}
	for i := range c.Done {
		d := &c.Done[i]
		f := fmt.Sprintf("done[%d]", i)
		if d.Reuse != nil {
			if c.Spec != SpecVersion2 || d.Check == "" || !d.Pure || !d.Reuse.Deterministic || len(d.Reuse.Inputs) == 0 || len(d.Reuse.EnvironmentFiles) == 0 || d.Reuse.MaxAgeSec < 1 || d.Reuse.MaxAgeSec > 3600 {
				errs = append(errs, ValidationError{f + ".reuse", "v2의 pure·deterministic 검사에 입력·환경 파일과 1~3600초 유효기간이 필요하다"})
			}
			for _, p := range d.Reuse.Inputs {
				if p == "" || filepath.IsAbs(p) || p == "." || strings.ContainsAny(p, "*?[\\") || p == ".." || strings.HasPrefix(filepath.Clean(p), "../") {
					errs = append(errs, ValidationError{f + ".reuse.inputs", "프로젝트 안의 구체적인 파일·폴더 경로가 필요하다"})
				}
			}
			for _, p := range d.Reuse.EnvironmentFiles {
				if p == "" || strings.ContainsAny(p, "*?[\\") {
					errs = append(errs, ValidationError{f + ".reuse.environment_files", "구체적인 환경 파일 경로가 필요하다"})
				}
			}
		}
		if (d.Check == "") == (d.Manual == "") {
			errs = append(errs, ValidationError{f, "check 또는 manual 중 정확히 하나가 필요하다"})
		}
		if d.Expect != "" && d.Expect != "exit0" {
			errs = append(errs, ValidationError{f + ".expect", "exit0만 지원한다"})
		}
		if d.Isolation != "" && d.Isolation != "worktree" {
			errs = append(errs, ValidationError{f + ".isolation", "worktree만 지원한다"})
		}
		if d.ID == "" {
			d.ID = fmt.Sprintf("c%d", i+1)
		}
		if ids[d.ID] {
			errs = append(errs, ValidationError{f + ".id", "중복 id"})
		}
		ids[d.ID] = true
	}
	for i, g := range append(append([]string{}, c.Scope.Allow...), c.Scope.Protect...) {
		if !doublestar.ValidatePattern(g) {
			errs = append(errs, ValidationError{fmt.Sprintf("scope[%d]", i), "잘못된 글롭: " + g})
		}
	}
	if c.Budget.KRW < 0 || c.Budget.Minutes < 0 || c.Budget.SameErrorRetries < 0 {
		errs = append(errs, ValidationError{"budget", "음수는 허용하지 않는다"})
	}
	return &c, errs
}

// Load reads the project's contract file. Missing file returns (nil, nil).
func Load(project string) (*Contract, []byte, error) {
	b, err := os.ReadFile(Path(project))
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	c, errs := Parse(b)
	if len(errs) > 0 {
		return nil, b, fmt.Errorf("%v", errs)
	}
	return c, b, nil
}

// ChecksHash hashes the machine checks.
func (c *Contract) ChecksHash() string {
	if c.Spec == SpecVersion2 {
		b, _ := json.Marshal(c.MachineChecks())
		return fp.Hash("checks-v2", string(b))
	}
	var parts []string
	for _, d := range c.Done {
		if d.Check != "" {
			parts = append(parts, d.ID+"\x00"+d.Check+"\x00"+d.Isolation+fmt.Sprint(d.Pure))
		}
	}
	return fp.Hash(append([]string{"checks"}, parts...)...)
}

// MachineChecks returns the machine-verifiable criteria.
func (c *Contract) MachineChecks() []Check {
	var out []Check
	for _, d := range c.Done {
		if d.Check != "" {
			out = append(out, d)
		}
	}
	return out
}

// NormalizePath returns a project-relative slash path, or "" when outside the root.
func NormalizePath(root, p string) string {
	if p == "" {
		return ""
	}
	p = filepath.ToSlash(p)
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		if root == "" {
			return ""
		}
		r, err := filepath.Rel(root, p)
		if err != nil {
			return ""
		}
		p = filepath.ToSlash(r)
	}
	p = path.Clean(p)
	if p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") {
		return ""
	}
	return p
}

// InScope reports whether a project-relative path may be changed. Paths
// outside the project root are always out of scope. Contract and samcheonpo
// files are always in scope.
func (c *Contract) InScope(rel string) bool {
	if rel == "" {
		return false
	}
	if strings.HasPrefix(rel, ".samcheonpo/") {
		return true
	}
	if c == nil || len(c.Scope.Allow) == 0 {
		return true
	}
	for _, g := range c.Scope.Allow {
		if ok, _ := doublestar.Match(g, rel); ok {
			return true
		}
	}
	return false
}

// Protected reports whether a path is protected.
func (c *Contract) Protected(rel string) bool {
	if c == nil || rel == "" {
		return false
	}
	for _, g := range c.Scope.Protect {
		if ok, _ := doublestar.Match(g, rel); ok {
			return true
		}
	}
	return false
}

// ForbidsTestChanges reports whether the user forbade test modification.
func (c *Contract) ForbidsTestChanges() bool {
	if c == nil {
		return false
	}
	for _, f := range c.Forbid {
		l := strings.ToLower(f)
		if strings.Contains(l, "테스트") || strings.Contains(l, "test") {
			return true
		}
	}
	return false
}

// AllowsTestWriting reports whether the goal or checks mention writing tests.
func (c *Contract) AllowsTestWriting() bool {
	if c == nil {
		return false
	}
	g := strings.ToLower(c.Goal)
	return strings.Contains(g, "테스트") || strings.Contains(g, "test")
}

// LoadAcceptance reads the acceptance state.
func LoadAcceptance(project string) Acceptance {
	b, err := os.ReadFile(filepath.Join(Dir(project), "contract.state.json"))
	if err != nil {
		return Acceptance{State: StateDraft}
	}
	var a Acceptance
	if json.Unmarshal(b, &a) != nil {
		return Acceptance{State: StateDraft}
	}
	return a
}

// SaveAcceptance writes the acceptance state atomically.
func SaveAcceptance(project string, a Acceptance) error {
	if err := os.MkdirAll(Dir(project), 0o755); err != nil {
		return err
	}
	a.MAC = ""
	mac, err := acceptanceMAC(project, a)
	if err != nil && a.State == StateAccepted {
		return fmt.Errorf("수락 기록 서명 실패: %w", err)
	}
	a.MAC = mac
	b, _ := json.MarshalIndent(a, "", "  ")
	f, err := os.CreateTemp(Dir(project), ".contract.state-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(Dir(project), "contract.state.json"))
}

// CurrentState resolves the acceptance state against the current file.
func CurrentState(project string, c *Contract, raw []byte) Acceptance {
	a := LoadAcceptance(project)
	if c == nil {
		if a.State == StateSkipped || a.RequestedGoal != "" {
			return a
		}
		return Acceptance{State: StateDraft}
	}
	if a.State == StateAccepted && (a.ChecksHash != c.ChecksHash() || a.AuthorityHash == "" || a.AuthorityHash != AuthorityDigest(c) || !validMAC(project, a)) {
		a.State = StateStale
	}
	return a
}

// AuthorityDigest hashes everything an acceptance authorizes. Unlike
// ChecksHash, which keys check results, it covers scope, protection, budget,
// forbidden actions and the goal.
func AuthorityDigest(c *Contract) string {
	b, _ := json.Marshal(c)
	return fp.Hash("authority-v1", string(b))
}

func acceptanceKeyPath() string { return filepath.Join(sockpath.Home(), "acceptance.key") }

// acceptanceKey returns the per-user signing key, creating it on first use.
func acceptanceKey() ([]byte, error) {
	p := acceptanceKeyPath()
	if b, err := os.ReadFile(p); err == nil && len(b) >= 32 {
		return b, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	// Write the key completely under a temporary name, then link it into
	// place: concurrent creators never observe a partial key.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".acceptance-*.key")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(key); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(tmp.Name(), p); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil || len(b) < 32 {
		return nil, fmt.Errorf("서명 키를 읽을 수 없다: %s", p)
	}
	return b, nil
}

func acceptanceMAC(project string, a Acceptance) (string, error) {
	key, err := acceptanceKey()
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(project)
	if err != nil {
		return "", err
	}
	a.MAC = ""
	b, _ := json.Marshal(a)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(root))
	m.Write([]byte{0})
	m.Write(b)
	return hex.EncodeToString(m.Sum(nil)), nil
}

func validMAC(project string, a Acceptance) bool {
	if a.MAC == "" {
		return false
	}
	want, err := acceptanceMAC(project, a)
	return err == nil && hmac.Equal([]byte(want), []byte(a.MAC))
}

// Accept marks the current contract accepted.
func Accept(project string, c *Contract, raw []byte, now time.Time) (Acceptance, error) {
	previous := LoadAcceptance(project)
	if previous.RequestedGoal != "" && previous.PreviousFileHash == fp.Hash(string(raw)) {
		return Acceptance{}, fmt.Errorf("목표 변경 요청이 아직 계약에 반영되지 않았다. 초안을 고친 뒤 수락해야 한다")
	}
	a := Acceptance{State: StateAccepted, ChecksHash: c.ChecksHash(), AuthorityHash: AuthorityDigest(c), FileHash: fp.Hash(string(raw)), AcceptedAt: now}
	return a, SaveAcceptance(project, a)
}

// RequestChange revokes the prior checks/scope authority before an agent edits
// the draft. It does not modify the contract or execute a check.
func RequestChange(project, goal string) (Acceptance, error) {
	_, raw, err := Load(project)
	if err != nil {
		return Acceptance{}, err
	}
	goal = strings.TrimSpace(goal)
	if goal == "" {
		goal = "사용자가 계약 수정을 요청했다. 수정 내용을 확인해야 한다."
	}
	a := Acceptance{State: StateDraft, RequestedGoal: goal, PreviousFileHash: fp.Hash(string(raw))}
	return a, SaveAcceptance(project, a)
}

// Candidate is a deterministic check candidate found in project manifests.
type Candidate struct {
	Command string `json:"command"`
	Source  string `json:"source"`
	Kind    string `json:"kind"` // test | build | lint | typecheck
}

var makeTargetRe = lazyre.New(`(?m)^(test|check|build|lint)\s*:`)
var ciRunRe = lazyre.New(`(?m)^\s*-?\s*run:\s*(.+)$`)

// Candidates extracts test/build commands from project manifests.
func Candidates(project string) []Candidate {
	var out []Candidate
	add := func(cmd, src, kind string) {
		for _, c := range out {
			if c.Command == cmd {
				return
			}
		}
		out = append(out, Candidate{cmd, src, kind})
	}
	if b, err := os.ReadFile(filepath.Join(project, "package.json")); err == nil {
		var pj struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(b, &pj) == nil {
			runner := "npm"
			for _, lock := range []struct{ f, r string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lockb", "bun"}, {"bun.lock", "bun"}} {
				if _, err := os.Stat(filepath.Join(project, lock.f)); err == nil {
					runner = lock.r
				}
			}
			keys := make([]string, 0, len(pj.Scripts))
			for k := range pj.Scripts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				kind := ""
				switch {
				case k == "test" || strings.HasPrefix(k, "test:"):
					kind = "test"
				case k == "build":
					kind = "build"
				case k == "lint":
					kind = "lint"
				case k == "typecheck" || k == "type-check" || k == "tsc":
					kind = "typecheck"
				}
				if kind != "" {
					add(runner+" run "+k, "package.json", kind)
				}
			}
		}
	}
	if _, err := os.Stat(filepath.Join(project, "go.mod")); err == nil {
		add("go test ./...", "go.mod", "test")
		add("go vet ./...", "go.mod", "lint")
	}
	if b, err := os.ReadFile(filepath.Join(project, "pyproject.toml")); err == nil {
		s := string(b)
		if strings.Contains(s, "pytest") {
			add("pytest -q", "pyproject.toml", "test")
		}
		if strings.Contains(s, "[tool.ruff") {
			add("ruff check .", "pyproject.toml", "lint")
		}
		if strings.Contains(s, "[tool.mypy") {
			add("mypy .", "pyproject.toml", "typecheck")
		}
	}
	for _, f := range []string{"pytest.ini", "setup.cfg", "tox.ini"} {
		if b, err := os.ReadFile(filepath.Join(project, f)); err == nil && strings.Contains(string(b), "pytest") {
			add("pytest -q", f, "test")
		}
	}
	if _, err := os.Stat(filepath.Join(project, "Cargo.toml")); err == nil {
		add("cargo test", "Cargo.toml", "test")
		add("cargo build", "Cargo.toml", "build")
	}
	for _, g := range []string{"gradlew", "build.gradle", "build.gradle.kts"} {
		if _, err := os.Stat(filepath.Join(project, g)); err == nil {
			r := "gradle"
			if _, err := os.Stat(filepath.Join(project, "gradlew")); err == nil {
				r = "./gradlew"
			}
			add(r+" test", g, "test")
			break
		}
	}
	if _, err := os.Stat(filepath.Join(project, "pom.xml")); err == nil {
		add("mvn -q test", "pom.xml", "test")
	}
	if b, err := os.ReadFile(filepath.Join(project, "Makefile")); err == nil {
		for _, m := range makeTargetRe.FindAllStringSubmatch(string(b), -1) {
			kind := m[1]
			if kind == "check" {
				kind = "test"
			}
			add("make "+m[1], "Makefile", kind)
		}
	}
	wf, _ := filepath.Glob(filepath.Join(project, ".github", "workflows", "*.y*ml"))
	sort.Strings(wf)
	for _, f := range wf {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, m := range ciRunRe.FindAllStringSubmatch(string(b), -1) {
			cmd := strings.Trim(strings.TrimSpace(m[1]), `"'`)
			l := strings.ToLower(cmd)
			if strings.Contains(l, "test") && !strings.Contains(cmd, "${{") && !strings.Contains(cmd, "|") && len(cmd) < 120 {
				add(cmd, filepath.ToSlash(filepath.Join(".github/workflows", filepath.Base(f))), "test")
			}
		}
	}
	return out
}

// DraftRequest renders the context injected on a new task: the
// template with prefilled check candidates; the agent only writes the goal,
// scope and the choice among candidates.
func DraftRequest(cands []Candidate, firstPrompt string) string {
	var b strings.Builder
	b.WriteString("[삼천포] 이 작업의 진척을 재기 위해 .samcheonpo/contract.yml 초안이 필요하다. ")
	b.WriteString("작업을 시작하기 전에 아래 양식으로 파일을 하나 쓰면 된다. 목표 한 문장, 고쳐도 되는 경로, 아래 후보 중 이 작업의 완료를 확인할 명령만 고르면 된다.\n")
	b.WriteString("```yaml\n")
	b.WriteString("goal: <사용자 요청을 한 문장으로>\n")
	b.WriteString("done:\n")
	if len(cands) == 0 {
		b.WriteString("  - check: \"<이 작업의 완료를 확인하는 명령>\"\n")
	}
	for _, c := range cands {
		fmt.Fprintf(&b, "  - check: %q   # %s (%s)\n", c.Command, c.Kind, c.Source)
	}
	b.WriteString("  - manual: <사람이 확인할 조건, 없으면 이 줄 삭제>\n")
	b.WriteString("scope:\n  allow: [\"<고쳐도 되는 경로 글롭>\"]\n  protect: []\n")
	b.WriteString("budget: {krw: 5000, minutes: 40, same_error_retries: 5}\n")
	b.WriteString("forbid: []\n```\n")
	b.WriteString("이 작업과 관계없는 후보 줄은 지우면 된다. 계약은 사용자가 확인한 뒤 진척 판정 기준이 된다.")
	return b.String()
}

// PlainSummary renders the contract for a non-developer user.
func PlainSummary(c *Contract) string {
	var b strings.Builder
	b.WriteString("AI는 이렇게 이해했습니다.\n")
	fmt.Fprintf(&b, "목표: %s\n", c.Goal)
	var crit []string
	for _, d := range c.Done {
		if d.Manual != "" {
			crit = append(crit, d.Manual+"(직접 확인)")
		} else {
			criterion := "`" + d.Check + "` 통과"
			if d.Reuse != nil {
				criterion += fmt.Sprintf(" (선언한 입력·환경이 같으면 %d초간 재사용; 외부 의존성·flaky 검사 제외)", d.Reuse.MaxAgeSec)
			}
			crit = append(crit, criterion)
		}
	}
	fmt.Fprintf(&b, "끝났다고 볼 기준: %s\n", strings.Join(crit, ", "))
	if len(c.Scope.Protect) > 0 || len(c.Forbid) > 0 {
		fmt.Fprintf(&b, "건드리지 않을 것: %s\n", strings.Join(append(append([]string{}, c.Scope.Protect...), c.Forbid...), ", "))
	}
	if c.Budget.KRW > 0 {
		fmt.Fprintf(&b, "예산: %s원\n", Comma(c.Budget.KRW))
	}
	b.WriteString("[/samcheonpo:accept 맞아요]  [/samcheonpo:edit 고칠래요]  [/samcheonpo:skip 계약 없이 진행]")
	return b.String()
}

// Comma formats an integer with thousands separators.
func Comma(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprint(n)
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
