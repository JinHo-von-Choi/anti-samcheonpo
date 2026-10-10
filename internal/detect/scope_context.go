package detect

import (
	"path"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
)

var absoluteScopeRe = lazyre.New("(?:^|[\\s`'\"(\\[])(/[\\p{L}\\p{N}_.@/-]+|[A-Za-z]:[/\\\\][\\p{L}\\p{N}_.@/\\\\-]+)")

// guessedScope records literal paths in the current request, never authority.
// An external file reference does not license its directory or siblings.
func (e *Engine) guessedScope(prompt string) []string {
	out := workScope(prompt)
	for _, m := range absoluteScopeRe.Get().FindAllStringSubmatchIndex(prompt, -1) {
		p := prompt[m[2]:m[3]]
		// Korean particles adjacent to a known file extension are prose,
		// not a license for a different file whose name includes the particle.
		for _, particle := range []string{"에서", "으로", "을", "를", "와", "과", "에", "로"} {
			candidate := strings.TrimSuffix(p, particle)
			if candidate != p && sourceExt[strings.TrimPrefix(strings.ToLower(path.Ext(candidate)), ".")] {
				p = candidate
				break
			}
		}
		start := strings.LastIndex(prompt[:m[2]], "\n") + 1
		end := strings.Index(prompt[m[3]:], "\n")
		if end < 0 {
			end = len(prompt)
		} else {
			end += m[3]
		}
		line := strings.ToLower(prompt[start:end])
		ambiguous := false
		for _, negative := range []string{"금지", "보호", "수정하지", "건드리지", "do not", "don't", "forbid", "protect"} {
			ambiguous = ambiguous || strings.Contains(line, negative)
		}
		if ambiguous {
			continue
		}
		p = path.Clean(strings.ReplaceAll(p, "\\", "/"))
		if p == "/" || len(strings.Trim(p, "/")) < 2 {
			continue
		}
		if rel := contract.NormalizePath(e.Root, p); rel != "" {
			p = rel
		}
		out = append(out, p)
	}
	return out
}
