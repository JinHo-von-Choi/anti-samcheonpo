package bench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Contamination is what a trial found of the agent's contact with the
// answer.
//
// A trial that read the grader is not being measured. It is being told the
// answer, and its pass says nothing about the agent. This record says
// whether that happened, so the reader of a result file can tell a
// measured trial from a spoiled one.
type Contamination struct {
	// Touched lists the paths the agent's tool calls reached that belong
	// to the grader, the answer or the rubric.
	Touched []string `json:"touched,omitempty"`
	// Copied lists the produced files whose content is the same as a
	// grader file.
	Copied []string `json:"copied,omitempty"`
	// Leaked lists the expected answers that appear in the produced
	// files.
	Leaked []string `json:"leaked,omitempty"`
	// Verdict is the worst of the three: clean, touched, copied or
	// leaked.
	Verdict string `json:"verdict"`
}

// Contamination verdicts.
const (
	// ContaminationClean means the agent never reached the answer.
	ContaminationClean = "clean"
	// ContaminationTouched means a tool call reached the grader's tree.
	ContaminationTouched = "touched"
	// ContaminationCopied means a grader file was copied into the
	// produced repository.
	ContaminationCopied = "copied"
	// ContaminationLeaked means an expected answer appears in the
	// produced repository.
	ContaminationLeaked = "leaked"
)

// minLeakLength is the shortest answer literal that is looked for.
//
// A one or two character answer appears in ordinary code by accident. A
// longer one appearing in the produced repository is a sign the answer was
// read, not a sign the agent solved the task.
const minLeakLength = 8

// protectedPaths returns the paths the trial monitor must not reach.
//
// The grader tree is the whole directory, not the two files in it, so a
// new file added to the grader is covered too.
func protectedPaths(graderRoot string) []string {
	if graderRoot == "" {
		return nil
	}
	return []string{graderRoot}
}

// DetectContamination inspects what the agent reached and what it left
// behind.
//
// The grader directory is the trial's own copy, which is the only copy the
// agent could have reached.
func DetectContamination(graderDir, repoDir string, toolPaths []string) Contamination {
	c := Contamination{Verdict: ContaminationClean}
	if graderDir == "" {
		return c
	}
	abs, err := filepath.Abs(graderDir)
	if err != nil {
		abs = graderDir
	}
	for _, p := range toolPaths {
		if p == "" {
			continue
		}
		candidate, err := filepath.Abs(p)
		if err != nil {
			candidate = p
		}
		if underPath(abs, candidate) {
			c.Touched = append(c.Touched, p)
		}
	}
	sort.Strings(c.Touched)
	c.Touched = uniqueStrings(c.Touched)
	c.Copied = copyMatches(graderDir, repoDir)
	c.Leaked = leakMatches(graderDir, repoDir)
	switch {
	case len(c.Copied) > 0:
		c.Verdict = ContaminationCopied
	case len(c.Leaked) > 0:
		c.Verdict = ContaminationLeaked
	case len(c.Touched) > 0:
		c.Verdict = ContaminationTouched
	}
	return c
}

// underPath reports whether path is inside root.
func underPath(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if root == path {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// uniqueStrings removes duplicates and keeps the order.
func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// fileHashes returns the hash of every file under a directory.
func fileHashes(root string) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(body)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	return out
}

// copyMatches lists the produced files whose content is the same as a
// grader file.
//
// A file the agent copied out of the grader is contamination even when
// the copy is renamed.
func copyMatches(graderDir, repoDir string) []string {
	if repoDir == "" {
		return nil
	}
	grader := fileHashes(graderDir)
	if len(grader) == 0 {
		return nil
	}
	byHash := map[string]string{}
	for rel, h := range grader {
		byHash[h] = rel
	}
	var out []string
	_ = filepath.WalkDir(repoDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(body)
		if _, ok := byHash[hex.EncodeToString(sum[:])]; ok {
			rel, err := filepath.Rel(repoDir, path)
			if err == nil {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// answerLiterals returns the expected answers of a task's cases.
//
// Only literals long enough to be a sign of a leak are returned. A short
// number appears in ordinary code by accident, and looking for it would
// report a leak that is not one.
func answerLiterals(graderDir string) []string {
	raw, err := os.ReadFile(filepath.Join(graderDir, "cases.json"))
	if err != nil {
		return nil
	}
	var spec struct {
		Cases []struct {
			Want any `json:"want"`
		} `json:"cases"`
	}
	if json.Unmarshal(raw, &spec) != nil {
		return nil
	}
	var out []string
	for _, c := range spec.Cases {
		out = append(out, literalsOf(c.Want)...)
	}
	sort.Strings(out)
	return uniqueStrings(out)
}

// literalsOf turns an expected value into the strings that would betray it.
func literalsOf(v any) []string {
	switch x := v.(type) {
	case string:
		if len([]rune(strings.TrimSpace(x))) >= minLeakLength {
			return []string{x}
		}
	case []any:
		var out []string
		for _, item := range x {
			out = append(out, literalsOf(item)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, item := range x {
			out = append(out, literalsOf(item)...)
		}
		return out
	}
	return nil
}

// leakMatches lists the expected answers that appear in the produced
// repository.
func leakMatches(graderDir, repoDir string) []string {
	if repoDir == "" {
		return nil
	}
	literals := answerLiterals(graderDir)
	if len(literals) == 0 {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(repoDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := string(body)
		for _, lit := range literals {
			if strings.Contains(text, lit) {
				rel, err := filepath.Rel(repoDir, path)
				if err == nil {
					out = append(out, filepath.ToSlash(rel))
				}
				break
			}
		}
		return nil
	})
	sort.Strings(out)
	return uniqueStrings(out)
}
