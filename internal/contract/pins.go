package contract

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testsel"
)

// maxPins caps the files one acceptance fixes.
const maxPins = 2000

// PinTests is the pin_tests field: "done" (the default, the tests the done
// checks run), "none", or a list of globs fixed in addition to them.
type PinTests struct {
	None  bool     `json:"none,omitempty"`
	Globs []string `json:"globs,omitempty"`
}

// UnmarshalYAML reads a word or a list of globs.
func (p *PinTests) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		switch strings.TrimSpace(n.Value) {
		case "none":
			p.None = true
		case "done", "":
		default:
			return fmt.Errorf("pin_tests: done, none or a list of globs")
		}
		return nil
	case yaml.SequenceNode:
		return n.Decode(&p.Globs)
	}
	return fmt.Errorf("pin_tests: done, none or a list of globs")
}

// Pin is one file fixed at acceptance.
type Pin struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	// Section marks a settings file of which only the test runner section
	// counts.
	Section bool `json:"section,omitempty"`
}

// ComputePins reads the files an acceptance fixes: the test files and
// collection settings of every done check, and the files the pin_tests
// globs match.
func ComputePins(project string, c *Contract) (pins []Pin, dirs []string, unknown []string) {
	if c == nil || (c.PinTests != nil && c.PinTests.None) {
		return nil, nil, nil
	}
	files, sections, dirSet := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, d := range c.Done {
		if d.Check == "" {
			continue
		}
		sel := testsel.Resolve(project, d.Check)
		if !sel.Known {
			unknown = append(unknown, d.ID)
			continue
		}
		for _, f := range sel.Files {
			files[f] = true
		}
		for _, f := range sel.Infra {
			if testsel.SectionFiles[path.Base(f)] {
				sections[f] = true
			} else {
				files[f] = true
			}
		}
		for _, x := range sel.Dirs {
			dirSet[x] = true
		}
	}
	if c.PinTests != nil {
		fsys := os.DirFS(project)
		for _, g := range c.PinTests.Globs {
			matches, _ := doublestar.Glob(fsys, g, doublestar.WithFilesOnly())
			for _, m := range matches {
				files[m] = true
			}
		}
	}
	add := func(p string, section bool) {
		if len(pins) >= maxPins {
			return
		}
		h, ok := pinHash(project, p, section)
		if ok {
			pins = append(pins, Pin{Path: p, Hash: h, Section: section})
		}
	}
	for _, p := range sortedKeys(files) {
		add(p, false)
	}
	for _, p := range sortedKeys(sections) {
		if !files[p] {
			add(p, true)
		}
	}
	return pins, sortedKeys(dirSet), unknown
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// pinHash hashes a fixed file; a missing file hashes as absent, so creating
// it later counts as a change.
func pinHash(project, rel string, section bool) (string, bool) {
	b, err := os.ReadFile(filepath.Join(project, filepath.FromSlash(rel)))
	if os.IsNotExist(err) {
		return "absent", true
	}
	if err != nil {
		return "", false
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	if section {
		text = testSection(path.Base(rel), text)
	}
	return fp.Hash("pin-v1", text), true
}

// testSection is the test runner part of a shared settings file: the
// [tool.pytest...] tables of pyproject.toml, or the [pytest] and
// [tool:pytest] sections of setup.cfg and tox.ini.
func testSection(name, text string) string {
	var b strings.Builder
	in := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			h := strings.Trim(t, "[] ")
			if name == "pyproject.toml" {
				in = strings.HasPrefix(h, "tool.pytest")
			} else {
				in = h == "pytest" || h == "tool:pytest"
			}
		}
		if in {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// PinDrift lists the fixed files whose content differs from the accepted
// one, and the collection files that appeared in a pinned folder since.
func PinDrift(project string, a Acceptance) []string {
	var out []string
	pinned := make(map[string]bool, len(a.Pins))
	for _, p := range a.Pins {
		pinned[p.Path] = true
		if h, ok := pinHash(project, p.Path, p.Section); !ok || h != p.Hash {
			out = append(out, p.Path)
		}
	}
	for _, d := range a.PinDirs {
		entries, err := os.ReadDir(filepath.Join(project, filepath.FromSlash(d)))
		if err != nil {
			continue
		}
		for _, e := range entries {
			rel := path.Join(d, e.Name())
			if e.IsDir() || pinned[rel] {
				continue
			}
			if IsCollectionFile(rel, func() string {
				b, _ := os.ReadFile(filepath.Join(project, filepath.FromSlash(rel)))
				return string(b)
			}) {
				out = append(out, rel)
			}
		}
	}
	return out
}

// IsCollectionFile reports a file that changes how the tests next to it are
// collected or run: conftest.py, pytest.ini, a jest or vitest config, or a
// Go test file that defines TestMain. content is read only for Go files.
func IsCollectionFile(rel string, content func() string) bool {
	n := path.Base(rel)
	if testsel.InfraNames[n] {
		return true
	}
	if strings.HasSuffix(n, "_test.go") && content != nil {
		return testsel.DefinesTestMain(content())
	}
	return false
}

// PinSummary tells, in a line or two, which tests an acceptance fixed and
// which done checks could not have their tests fixed.
func PinSummary(a Acceptance) string {
	var b strings.Builder
	if n := len(a.Pins); n > 0 {
		fmt.Fprintf(&b, lang.T("완료 조건이 실행하는 시험과 시험 설정 파일 %d개를 고정했다. 이 파일을 바꾸려면 계약을 고쳐 다시 수락해야 한다.\n"), n)
	}
	if len(a.PinUnknown) > 0 {
		fmt.Fprintf(&b, lang.T("완료 조건 %s는 어떤 시험을 실행하는지 명령만으로 알 수 없어 고정하지 않았다.\n"), strings.Join(a.PinUnknown, ", "))
	}
	return b.String()
}

// CurrentStateWithPins is CurrentState that also marks an acceptance stale
// when a fixed test file changed since.
func CurrentStateWithPins(project string, c *Contract, raw []byte) Acceptance {
	a := CurrentState(project, c, raw)
	if a.State == StateAccepted && len(PinDrift(project, a)) > 0 {
		a.State = StateStale
	}
	return a
}
