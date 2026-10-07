package detect

import (
	"crypto/sha256"
	"encoding/hex"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// semBufN is the number of recent failed states kept per file.
const semBufN = 5

// semCodeCap bounds one file considered for semantic fingerprinting so the
// hook path stays inside its latency budget.
const semCodeCap = 256 << 10

type semEntry struct {
	fp  string
	raw string // sha256 of the original bytes
}

// SemanticOscillationDetector keeps the normalized fingerprints of the last
// semBufN failed states per file and reports a return to an earlier state
// whose bytes changed but whose structure did not.
type SemanticOscillationDetector struct {
	mu  sync.Mutex
	buf map[string][]semEntry
}

// NewSemanticOscillationDetector creates an empty detector.
func NewSemanticOscillationDetector() *SemanticOscillationDetector {
	return &SemanticOscillationDetector{buf: map[string][]semEntry{}}
}

// RecordFailure stores the failed state of path. Code beyond semCodeCap or
// too short to be a source file is ignored.
func (d *SemanticOscillationDetector) RecordFailure(path, code string) {
	fpStr, ok := semanticFingerprint(code)
	if !ok {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	buf := append(d.buf[path], semEntry{fp: fpStr, raw: rawHash(code)})
	if len(buf) > semBufN {
		buf = buf[len(buf)-semBufN:]
	}
	d.buf[path] = buf
}

// CheckOscillation reports whether code matches a recorded failed state of
// path while its own bytes differ, returning the buffer index of that state.
func (d *SemanticOscillationDetector) CheckOscillation(path, code string) (bool, int) {
	fpStr, ok := semanticFingerprint(code)
	if !ok {
		return false, -1
	}
	raw := rawHash(code)
	d.mu.Lock()
	defer d.mu.Unlock()
	buf := d.buf[path]
	for i := len(buf) - 1; i >= 0; i-- {
		if buf[i].fp == fpStr && buf[i].raw != raw {
			return true, i
		}
	}
	return false, -1
}

// Clear drops all recorded states; a passing verification ends the sequence.
func (d *SemanticOscillationDetector) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.buf = map[string][]semEntry{}
}

// semanticFingerprint reduces source code to a token stream with comments
// stripped, identifiers replaced by IDENT and literals by LIT, so renaming,
// reformatting and comment edits collapse to one fingerprint while a changed
// operator sequence or literal count still differs.
func semanticFingerprint(code string) (fp string, ok bool) {
	if len(code) == 0 || len(code) > semCodeCap {
		return "", false
	}
	defer func() {
		if recover() != nil {
			fp, ok = "", false
		}
	}()
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(code))
	var s scanner.Scanner
	s.Init(file, []byte(code), nil, scanner.ScanComments)
	var b strings.Builder
	for {
		_, tok, _ := s.Scan()
		if tok == token.EOF {
			break
		}
		switch tok {
		case token.COMMENT, token.ILLEGAL:
			continue
		case token.IDENT:
			b.WriteString("IDENT")
		case token.INT, token.FLOAT, token.IMAG, token.CHAR, token.STRING:
			b.WriteString("LIT")
		default:
			b.WriteString(tok.String())
		}
		b.WriteByte(0)
	}
	if b.Len() == 0 {
		return "", false
	}
	return b.String(), true
}

// BufferedPaths lists the files that still hold recorded failed states, sorted.
func (d *SemanticOscillationDetector) BufferedPaths() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	paths := make([]string, 0, len(d.buf))
	for p, buf := range d.buf {
		if len(buf) > 0 {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths
}

func rawHash(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// recordSemanticFailures stores the current on-disk state of every file
// touched since the last passing verification as a failed state.
func (e *Engine) recordSemanticFailures() {
	st := e.St
	for _, p := range sortedAttemptFiles(st.attemptFiles) {
		if code, ok := e.readWorkFile(p); ok {
			st.semOsc.RecordFailure(p, code)
		}
	}
}

// semanticPingPong checks the current on-disk state of every file that still
// has recorded failed states against those states.
func (e *Engine) semanticPingPong() *Signal {
	st := e.St
	for _, p := range st.semOsc.BufferedPaths() {
		code, ok := e.readWorkFile(p)
		if !ok {
			continue
		}
		if hit, idx := st.semOsc.CheckOscillation(p, code); hit {
			return &Signal{Detector: "S2", Rule: "s2.semantic_oscillation", Confidence: 0.85, Level: L1,
				Facts: map[string]any{"path": p, "prev_index": idx, "blocked": true}}
		}
	}
	return nil
}

// readWorkFile loads a workspace-relative file, refusing absolute paths,
// traversal outside the root and files above semCodeCap.
func (e *Engine) readWorkFile(p string) (string, bool) {
	if e.Root == "" || p == "" || strings.Contains(p, "\x00") || filepath.IsAbs(p) {
		return "", false
	}
	full := filepath.Clean(filepath.Join(e.Root, filepath.FromSlash(p)))
	root := filepath.Clean(e.Root)
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", false
	}
	fi, err := os.Stat(full)
	if err != nil || fi.IsDir() || fi.Size() > semCodeCap {
		return "", false
	}
	b, err := os.ReadFile(full)
	if err != nil || len(b) > semCodeCap {
		return "", false
	}
	return string(b), true
}

func sortedAttemptFiles(attempt map[string]bool) []string {
	files := make([]string, 0, len(attempt))
	for p := range attempt {
		files = append(files, p)
	}
	sort.Strings(files)
	return files
}
