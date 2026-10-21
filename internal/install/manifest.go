package install

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
)

// ManifestVersion is the format version of the plugin bundle manifest.
const ManifestVersion = "plugin-manifest/1"

// Manifest is the integrity record of one installed plugin bundle.
//
// The digest covers every file's path, hash and size, so a changed, a
// missing or an added file changes the digest. The digest is kept in the
// registry under the monitor's own home, which is the trust anchor.
//
// There is no third-party signature here and none is claimed. This proves
// the bundle on disk is the bundle that was installed; it does not prove
// the bundle was the one that was published.
type Manifest struct {
	Version   string         `json:"version"`
	Plugin    string         `json:"plugin"`
	Root      string         `json:"root"`
	CreatedAt time.Time      `json:"created_at"`
	Files     []ManifestFile `json:"files"`
	// Digest is the content address of the file list.
	Digest string `json:"digest"`
	// Complete marks a manifest that covers every file under Root. Only
	// then can an added file be noticed.
	Complete bool `json:"complete,omitempty"`
}

// ManifestFile is one file of a bundle.
type ManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest problem kinds.
const (
	// ProblemMissing is a file the manifest lists that is gone.
	ProblemMissing = "missing"
	// ProblemChanged is a file whose content no longer matches.
	ProblemChanged = "changed"
	// ProblemAdded is a file under a complete manifest that the manifest
	// does not list.
	ProblemAdded = "added"
	// ProblemUnreadable is a file that could not be read.
	ProblemUnreadable = "unreadable"
)

// ManifestProblem is one integrity failure of a bundle.
type ManifestProblem struct {
	Plugin string `json:"plugin"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

// Registry is the set of bundles the monitor installed.
type Registry struct {
	Version string     `json:"version"`
	Bundles []Manifest `json:"bundles"`
}

// RegistryPath is where the manifest registry lives.
func RegistryPath() string { return filepath.Join(config.Home(), "plugin-manifests.json") }

// BuildManifest records the files of one bundle.
//
// The files are given as absolute paths. Their paths relative to root
// are what the manifest keeps, so the record survives the home moving.
func BuildManifest(plugin, root string, files []string, complete bool) (Manifest, error) {
	m := Manifest{Version: ManifestVersion, Plugin: plugin, Root: root, CreatedAt: time.Now().UTC(), Complete: complete}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return m, err
	}
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return m, err
		}
		rel, err := filepath.Rel(absRoot, abs)
		if err != nil {
			return m, err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return m, err
		}
		body, err := os.ReadFile(abs)
		if err != nil {
			return m, err
		}
		m.Files = append(m.Files, ManifestFile{Path: filepath.ToSlash(rel), SHA256: hashOf(body), Size: info.Size()})
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	m.Digest = ManifestDigest(m.Files)
	return m, nil
}

// BuildDirManifest records every file under a directory.
func BuildDirManifest(plugin, root string) (Manifest, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("일반 파일이 아닌 플러그인 경로: %s", path)
		}
		// A runtime cache is not an installed file, so it is not recorded.
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if runtimeArtifact(rel) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}
	return BuildManifest(plugin, root, files, true)
}

// runtimeArtifact reports whether a path under a bundle root is a file the
// runtime writes on its own.
//
// A Python plugin that is imported leaves a bytecode cache behind. That cache
// is derived from the recorded source, not installed by the monitor, so it
// must not be recorded as an installed file and must not be reported as an
// added one.
func runtimeArtifact(rel string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == "__pycache__" {
			return true
		}
	}
	return false
}

// pycSourceName maps a bytecode cache file name to the source file name it
// was compiled from.
//
// Python names the cache after the source module and the interpreter that
// wrote it: "init.cpython-314.pyc", "init.cpython-314.opt-1.pyc".
func pycSourceName(name string) (string, bool) {
	parts := strings.Split(name, ".")
	if len(parts) < 3 {
		return "", false
	}
	idx := -1
	for i, p := range parts {
		if strings.HasPrefix(p, "cpython-") {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return "", false
	}
	return strings.Join(parts[:idx], ".") + ".py", true
}

// checkBytecodeCache verifies a Python bytecode cache against the source it
// was compiled from.
//
// The cache is not part of the installed bundle, so it is neither recorded
// nor reported as an added file. It still has to belong to a recorded source
// and still has to be current for that source: a cache that carries another
// source's timestamp or size was built from something else, and that is a
// real difference worth reporting.
//
// A hash-based cache carries the source hash instead of a timestamp, and that
// hash cannot be recomputed here, so such a cache is accepted on the
// strength of its source being intact. That limit is stated here rather than
// hidden.
func checkBytecodeCache(plugin, root, rel string, listed map[string]bool) (ManifestProblem, bool) {
	slashed := filepath.ToSlash(rel)
	dir, file := "", slashed
	if i := strings.LastIndex(slashed, "/"); i >= 0 {
		dir, file = slashed[:i+1], slashed[i+1:]
	}
	cacheDir := strings.TrimSuffix(dir, "/")
	if !strings.HasSuffix(cacheDir, "__pycache__") {
		return ManifestProblem{}, false
	}
	srcDir := ""
	if cacheDir != "__pycache__" {
		srcDir = strings.TrimSuffix(cacheDir, "/__pycache__") + "/"
	}
	name, ok := pycSourceName(file)
	if !ok {
		// The runtime only writes caches it can name after a source. A file
		// it cannot name was not written by the runtime.
		return ManifestProblem{Plugin: plugin, Path: slashed, Kind: ProblemAdded,
			Detail: "캐시 규약에 맞지 않는 파일이다"}, true
	}
	srcRel := srcDir + name
	if !listed[srcRel] {
		// The cache belongs to code that was not installed.
		return ManifestProblem{Plugin: plugin, Path: slashed, Kind: ProblemAdded,
			Detail: "기록에 없는 원본의 캐시다"}, true
	}
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(slashed)))
	if err != nil {
		return ManifestProblem{Plugin: plugin, Path: slashed, Kind: ProblemUnreadable, Detail: err.Error()}, true
	}
	if len(body) < 16 {
		return ManifestProblem{Plugin: plugin, Path: slashed, Kind: ProblemAdded,
			Detail: "캐시 형식이 아니다"}, true
	}
	if flags := binary.LittleEndian.Uint32(body[4:8]); flags != 0 {
		// A hash-based cache cannot be re-derived here.
		return ManifestProblem{}, false
	}
	srcMtime := binary.LittleEndian.Uint32(body[8:12])
	srcSize := binary.LittleEndian.Uint32(body[12:16])
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(srcRel)))
	if err != nil {
		return ManifestProblem{}, false
	}
	if uint32(info.Size()) != srcSize || uint32(info.ModTime().Unix()) != srcMtime {
		return ManifestProblem{Plugin: plugin, Path: slashed, Kind: ProblemChanged,
			Detail: "기록된 원본과 캐시가 맞지 않는다"}, true
	}
	return ManifestProblem{}, false
}

// hashOf returns the sha256 of a body as hex.
func hashOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// ManifestDigest returns the content address of a file list.
//
// The digest covers the path, the hash and the size of every file, in
// path order. A file that is renamed, edited or resized changes it.
func ManifestDigest(files []ManifestFile) string {
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%s\x00%d\n", f.Path, f.SHA256, f.Size)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyManifest checks a bundle against its manifest.
//
// It returns every problem it found. An empty result means the bundle on
// disk is the bundle that was recorded.
func VerifyManifest(m Manifest) []ManifestProblem {
	var problems []ManifestProblem
	root := m.Root
	if root == "" {
		return []ManifestProblem{{Plugin: m.Plugin, Kind: ProblemUnreadable, Detail: "매니페스트에 루트 경로가 없다"}}
	}
	listed := map[string]bool{}
	for _, f := range m.Files {
		listed[f.Path] = true
		p := filepath.Join(root, filepath.FromSlash(f.Path))
		body, err := os.ReadFile(p)
		if err != nil {
			kind := ProblemMissing
			if !os.IsNotExist(err) {
				kind = ProblemUnreadable
			}
			problems = append(problems, ManifestProblem{Plugin: m.Plugin, Path: f.Path, Kind: kind, Detail: err.Error()})
			continue
		}
		if got := hashOf(body); got != f.SHA256 {
			problems = append(problems, ManifestProblem{Plugin: m.Plugin, Path: f.Path, Kind: ProblemChanged,
				Detail: "기록된 해시와 다르다"})
		}
	}
	if !m.Complete {
		return problems
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		slashed := filepath.ToSlash(rel)
		if listed[slashed] {
			return nil
		}
		if runtimeArtifact(slashed) {
			// A runtime cache is derived from a recorded source file, so it
			// is not an added file. It is still checked against that source.
			if p, ok := checkBytecodeCache(m.Plugin, root, rel, listed); ok {
				problems = append(problems, p)
			}
			return nil
		}
		problems = append(problems, ManifestProblem{Plugin: m.Plugin, Path: slashed, Kind: ProblemAdded})
		return nil
	})
	return problems
}

// VerifyRegistry checks every bundle in a registry.
func VerifyRegistry(r Registry) []ManifestProblem {
	var problems []ManifestProblem
	for _, m := range r.Bundles {
		problems = append(problems, VerifyManifest(m)...)
	}
	return problems
}

// LoadRegistry reads the manifest registry.
//
// A missing registry is an empty registry, not an error: nothing was
// installed through the recording path.
func LoadRegistry() (Registry, error) {
	r := Registry{Version: ManifestVersion}
	raw, err := os.ReadFile(RegistryPath())
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return Registry{Version: ManifestVersion}, err
	}
	if r.Version == "" {
		r.Version = ManifestVersion
	}
	return r, nil
}

// SaveRegistry writes the manifest registry.
func SaveRegistry(r Registry) error {
	if r.Version == "" {
		r.Version = ManifestVersion
	}
	sort.Slice(r.Bundles, func(i, j int) bool { return r.Bundles[i].Plugin < r.Bundles[j].Plugin })
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(RegistryPath(), append(b, '\n'), 0600)
}

// RecordManifest adds or replaces a bundle's manifest.
//
// The registry is the trust anchor: a bundle that is not in it is not
// covered, and a bundle in it that no longer matches is reported.
func RecordManifest(m Manifest) error {
	r, err := LoadRegistry()
	if err != nil {
		return err
	}
	for i := range r.Bundles {
		if r.Bundles[i].Plugin == m.Plugin {
			r.Bundles[i] = m
			return SaveRegistry(r)
		}
	}
	r.Bundles = append(r.Bundles, m)
	return SaveRegistry(r)
}

// ForgetManifest removes a bundle's manifest, which is what uninstalling
// a bundle does.
func ForgetManifest(plugin string) error {
	r, err := LoadRegistry()
	if err != nil {
		return err
	}
	kept := r.Bundles[:0]
	for _, b := range r.Bundles {
		if b.Plugin != plugin {
			kept = append(kept, b)
		}
	}
	r.Bundles = kept
	return SaveRegistry(r)
}

// ManifestSummary describes one bundle for a person.
type ManifestSummary struct {
	Plugin  string    `json:"plugin"`
	Root    string    `json:"root"`
	Files   int       `json:"files"`
	Digest  string    `json:"digest"`
	Created time.Time `json:"created_at"`
	// Problems is how many integrity failures the bundle has.
	Problems int `json:"problems"`
}

// SummarizeManifest describes the registry for a person.
func SummarizeManifest(r Registry) []ManifestSummary {
	out := make([]ManifestSummary, 0, len(r.Bundles))
	for _, m := range r.Bundles {
		out = append(out, ManifestSummary{Plugin: m.Plugin, Root: m.Root, Files: len(m.Files), Digest: m.Digest,
			Created: m.CreatedAt, Problems: len(VerifyManifest(m))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Plugin < out[j].Plugin })
	return out
}

// ProblemText renders a problem for a person.
func ProblemText(p ManifestProblem) string {
	switch p.Kind {
	case ProblemMissing:
		return "사라진 파일: " + p.Path
	case ProblemChanged:
		return "내용이 바뀐 파일: " + p.Path
	case ProblemAdded:
		return "기록에 없는 파일: " + p.Path
	case ProblemUnreadable:
		return "읽을 수 없는 파일: " + p.Path
	}
	return p.Kind + ": " + p.Path
}

// ErrNoManifest is returned when a bundle has no manifest to verify.
var ErrNoManifest = errors.New("매니페스트 기록이 없다")

// ManifestFor returns the manifest of one plugin bundle.
func ManifestFor(r Registry, plugin string) (Manifest, error) {
	for _, m := range r.Bundles {
		if m.Plugin == plugin {
			return m, nil
		}
	}
	return Manifest{}, ErrNoManifest
}

// bundleRootName names a bundle root for display.
func bundleRootName(root string) string {
	if root == "" {
		return ""
	}
	return strings.TrimSuffix(root, string(filepath.Separator))
}
