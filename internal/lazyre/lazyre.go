// Package lazyre compiles regular expressions on first use, so the hook
// client path (which shares the binary) does not pay for every pattern at
// process start.
package lazyre

import (
	"regexp"
	"sync"
)

// RE is a lazily compiled regular expression.
type RE struct {
	pat  string
	once sync.Once
	re   *regexp.Regexp
}

// New returns a lazily compiled expression; an invalid pattern panics on first use.
func New(pat string) *RE { return &RE{pat: pat} }

// Get compiles (once) and returns the expression.
func (r *RE) Get() *regexp.Regexp {
	r.once.Do(func() { r.re = regexp.MustCompile(r.pat) })
	return r.re
}

func (r *RE) MatchString(s string) bool                  { return r.Get().MatchString(s) }
func (r *RE) FindString(s string) string                 { return r.Get().FindString(s) }
func (r *RE) FindStringSubmatch(s string) []string       { return r.Get().FindStringSubmatch(s) }
func (r *RE) FindStringIndex(s string) []int             { return r.Get().FindStringIndex(s) }
func (r *RE) FindAllString(s string, n int) []string     { return r.Get().FindAllString(s, n) }
func (r *RE) FindAllStringIndex(s string, n int) [][]int { return r.Get().FindAllStringIndex(s, n) }
func (r *RE) FindAllStringSubmatch(s string, n int) [][]string {
	return r.Get().FindAllStringSubmatch(s, n)
}
func (r *RE) ReplaceAllString(s, repl string) string { return r.Get().ReplaceAllString(s, repl) }
func (r *RE) ReplaceAllStringFunc(s string, f func(string) string) string {
	return r.Get().ReplaceAllStringFunc(s, f)
}
