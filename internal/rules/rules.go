// Package rules loads versioned rule data that extends the built-in
// classifiers without rebuilding the binary.
package rules

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sockpath"
)

//go:embed default.yml
var builtin []byte

// List is a pattern list with its own test cases.
type List struct {
	Patterns []string `yaml:"patterns"`
	Cases    struct {
		Match   []string `yaml:"match"`
		NoMatch []string `yaml:"no_match"`
	} `yaml:"cases"`
	compiled []*regexp.Regexp
}

// AutoContinue is an auto-continue tool's state file.
type AutoContinue struct {
	Path string `yaml:"path"`
	Stop string `yaml:"stop"`
}

// Set is the rule data.
type Set struct {
	Version           int            `yaml:"version"`
	VerifyCommands    List           `yaml:"verify_commands"`
	FailedTests       List           `yaml:"failed_tests"`
	ForcedPrompts     List           `yaml:"forced_prompts"`
	AutoContinueFiles []AutoContinue `yaml:"auto_continue_files"`
}

func (l *List) compile(name string) error {
	l.compiled = nil
	for _, p := range l.Patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("%s: %q: %w", name, p, err)
		}
		l.compiled = append(l.compiled, re)
	}
	return nil
}

// Match reports whether any pattern matches.
func (l *List) Match(s string) bool {
	for _, re := range l.compiled {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// Captures returns capture group 1 of every matching pattern.
func (l *List) Captures(s string) []string {
	var out []string
	for _, re := range l.compiled {
		if m := re.FindStringSubmatch(s); len(m) > 1 {
			out = append(out, strings.TrimSpace(m[1]))
		}
	}
	return out
}

// Check compiles every list and runs its cases.
func (s *Set) Check() []string {
	var bad []string
	for name, l := range map[string]*List{"verify_commands": &s.VerifyCommands, "failed_tests": &s.FailedTests, "forced_prompts": &s.ForcedPrompts} {
		if err := l.compile(name); err != nil {
			bad = append(bad, err.Error())
			continue
		}
		for _, c := range l.Cases.Match {
			ok := l.Match(c)
			if !ok {
				for _, ln := range strings.Split(c, "\n") {
					if l.Match(ln) {
						ok = true
					}
				}
			}
			if !ok {
				bad = append(bad, fmt.Sprintf("%s: %q must match", name, c))
			}
		}
		for _, c := range l.Cases.NoMatch {
			if l.Match(c) {
				bad = append(bad, fmt.Sprintf("%s: %q must not match", name, c))
			}
		}
	}
	return bad
}

// Parse reads rule data.
func Parse(b []byte) (*Set, error) {
	var s Set
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, err
	}
	if s.Version != 1 {
		return nil, fmt.Errorf("지원하지 않는 규칙 버전 %d", s.Version)
	}
	return &s, nil
}

func merge(a, b *Set) {
	a.VerifyCommands.Patterns = append(a.VerifyCommands.Patterns, b.VerifyCommands.Patterns...)
	a.FailedTests.Patterns = append(a.FailedTests.Patterns, b.FailedTests.Patterns...)
	a.ForcedPrompts.Patterns = append(a.ForcedPrompts.Patterns, b.ForcedPrompts.Patterns...)
	a.AutoContinueFiles = append(a.AutoContinueFiles, b.AutoContinueFiles...)
	a.VerifyCommands.Cases.Match = append(a.VerifyCommands.Cases.Match, b.VerifyCommands.Cases.Match...)
	a.VerifyCommands.Cases.NoMatch = append(a.VerifyCommands.Cases.NoMatch, b.VerifyCommands.Cases.NoMatch...)
	a.FailedTests.Cases.Match = append(a.FailedTests.Cases.Match, b.FailedTests.Cases.Match...)
	a.FailedTests.Cases.NoMatch = append(a.FailedTests.Cases.NoMatch, b.FailedTests.Cases.NoMatch...)
	a.ForcedPrompts.Cases.Match = append(a.ForcedPrompts.Cases.Match, b.ForcedPrompts.Cases.Match...)
	a.ForcedPrompts.Cases.NoMatch = append(a.ForcedPrompts.Cases.NoMatch, b.ForcedPrompts.Cases.NoMatch...)
}

// Load returns the built-in rules merged with the user file. An invalid user
// file is reported and ignored so a typo never stops the harness.
func Load(extra ...string) (*Set, []string) {
	s, err := Parse(builtin)
	if err != nil {
		panic("built-in rules: " + err.Error())
	}
	var warns []string
	files := append([]string{filepath.Join(sockpath.Home(), "rules.yml")}, extra...)
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		u, err := Parse(b)
		if err != nil {
			warns = append(warns, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		if bad := u.Check(); len(bad) > 0 {
			warns = append(warns, fmt.Sprintf("%s: %s", p, strings.Join(bad, "; ")))
			continue
		}
		merge(s, u)
	}
	_ = s.Check()
	return s, warns
}

var (
	once    sync.Once
	current *Set
)

// Current returns the process-wide rule set (built-in plus user file).
func Current() *Set {
	once.Do(func() { current, _ = Load() })
	return current
}
