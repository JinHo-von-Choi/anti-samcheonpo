// Package pathnorm turns the spellings one program or file has on different
// systems into a single comparable form. Commands and paths reach the harness
// as text written by an agent or a tool, so "samcheonpo", "SAMCHEONPO.EXE" and
// "C:\tools\samcheonpo.exe" must all be recognised as the same program.
package pathnorm

import (
	"path/filepath"
	"strings"
)

var executableSuffixes = []string{".exe", ".cmd", ".bat", ".com"}

// CommandBase returns the program name of a command word: the last element of
// the path in either separator style, lower case, without an executable
// suffix.
func CommandBase(word string) string {
	word = strings.TrimSpace(word)
	if i := strings.LastIndexAny(word, `/\`); i >= 0 {
		word = word[i+1:]
	}
	word = strings.ToLower(word)
	for _, suffix := range executableSuffixes {
		if strings.HasSuffix(word, suffix) {
			return strings.TrimSuffix(word, suffix)
		}
	}
	return word
}

// HasExecutableSuffix reports whether word names a Windows program by suffix.
func HasExecutableSuffix(word string) bool {
	lower := strings.ToLower(word)
	for _, suffix := range executableSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// IsWindowsAbs reports whether p starts with a drive letter or is a UNC or
// device path, whatever system the harness runs on.
func IsWindowsAbs(p string) bool {
	if len(p) >= 2 && p[1] == ':' && (p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z') {
		return true
	}
	return strings.HasPrefix(p, `\\`)
}

// IsAbs reports whether a slash-separated path is absolute in either
// convention: rooted at "/", or a drive letter or UNC path.
func IsAbs(p string) bool { return strings.HasPrefix(p, "/") || IsWindowsAbs(p) }

// IsTempPath reports whether a slash-separated absolute path lies in a
// scratch directory of any supported system.
func IsTempPath(p string) bool {
	if strings.HasPrefix(p, "/tmp/") || strings.HasPrefix(p, "/var/tmp/") ||
		strings.HasPrefix(p, "/private/tmp/") || strings.HasPrefix(p, "/var/folders/") || strings.HasPrefix(p, "/private/var/folders/") {
		return true
	}
	lower := strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
	return IsWindowsAbs(p) && (strings.Contains(lower, "/appdata/local/temp/") || strings.Contains(lower, "/windows/temp/"))
}

// Absolute returns the absolute form of p with symbolic links resolved and,
// on Windows, the spelling the file system itself stores. Two spellings of
// one directory (/var/x and /private/var/x on macOS, C:\\Proj and c:\\proj on
// Windows) come out equal, which is what an identity needs.
func Absolute(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return canonical(abs), nil
}

// Spellings lists the forms under which a directory may have been recorded
// before identities were canonical: the canonical form first, then the plain
// absolute form when it differs.
func Spellings(p string) ([]string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, err
	}
	out := []string{canonical(abs)}
	if abs != out[0] {
		out = append(out, abs)
	}
	return out, nil
}

// canonical resolves the longest existing prefix of abs and appends the rest
// as written, so a path that does not exist yet still gets a stable form.
func canonical(abs string) string {
	cur, rest := abs, ""
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
