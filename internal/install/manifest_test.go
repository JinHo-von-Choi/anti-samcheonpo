package install

import (
	"os"
	"path/filepath"
	"testing"
)

// writeBundle writes a small plugin bundle and returns its root.
func writeBundle(t *testing.T, root string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The manifest records what was installed, and the verification says
// whether the files on disk are still those files.
func TestManifestDetectsChangedMissingAndAddedFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bundle")
	writeBundle(t, root, map[string]string{"hooks.json": "{}", "README.md": "hello"})
	m, err := BuildDirManifest("claude-code", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 2 || m.Digest == "" {
		t.Fatalf("manifest %+v", m)
	}
	if problems := VerifyManifest(m); len(problems) != 0 {
		t.Fatalf("problems %+v", problems)
	}
	// A changed file is noticed.
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	problems := VerifyManifest(m)
	if len(problems) != 1 || problems[0].Kind != ProblemChanged || problems[0].Path != "README.md" {
		t.Fatalf("problems %+v", problems)
	}
	// A missing file is noticed.
	if err := os.Remove(filepath.Join(root, "README.md")); err != nil {
		t.Fatal(err)
	}
	problems = VerifyManifest(m)
	kinds := map[string]int{}
	for _, p := range problems {
		kinds[p.Kind]++
	}
	if kinds[ProblemMissing] != 1 {
		t.Fatalf("problems %+v", problems)
	}
	// A file that was not installed is noticed when the manifest covers
	// the whole directory.
	writeBundle(t, root, map[string]string{"extra.sh": "echo"})
	problems = VerifyManifest(m)
	found := false
	for _, p := range problems {
		if p.Kind == ProblemAdded && p.Path == "extra.sh" {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems %+v", problems)
	}
}

// The digest is the content address of the file list. A different file
// list is a different digest.
func TestManifestDigestCoversPathHashAndSize(t *testing.T) {
	a := ManifestFile{Path: "a.txt", SHA256: "aa", Size: 1}
	b := ManifestFile{Path: "b.txt", SHA256: "bb", Size: 2}
	if ManifestDigest([]ManifestFile{a, b}) == ManifestDigest([]ManifestFile{b, a}) {
		t.Fatal("the digest does not depend on the order")
	}
	if ManifestDigest([]ManifestFile{a, b}) == ManifestDigest([]ManifestFile{a}) {
		t.Fatal("the digest ignores a file")
	}
	changed := a
	changed.Size = 2
	if ManifestDigest([]ManifestFile{a, b}) == ManifestDigest([]ManifestFile{changed, b}) {
		t.Fatal("the digest ignores the size")
	}
}

// The registry is the trust anchor: what was recorded can be read back and
// verified later.
func TestRegistryRecordsAndVerifiesBundles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(home, ".samcheonpo"))
	root := filepath.Join(home, "bundle")
	writeBundle(t, root, map[string]string{"plugin.yaml": "name: samcheonpo"})
	m, err := BuildDirManifest("hermes", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordManifest(m); err != nil {
		t.Fatal(err)
	}
	registry, err := LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Bundles) != 1 {
		t.Fatalf("registry %+v", registry)
	}
	if problems := VerifyRegistry(registry); len(problems) != 0 {
		t.Fatalf("problems %+v", problems)
	}
	// A second bundle does not replace the first.
	other := filepath.Join(home, "other")
	writeBundle(t, other, map[string]string{"index.js": "x"})
	om, err := BuildDirManifest("openclaw", other)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordManifest(om); err != nil {
		t.Fatal(err)
	}
	registry, err = LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Bundles) != 2 {
		t.Fatalf("registry %+v", registry)
	}
	// Uninstalling a bundle forgets its record.
	if err := ForgetManifest("hermes"); err != nil {
		t.Fatal(err)
	}
	registry, err = LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Bundles) != 1 || registry.Bundles[0].Plugin != "openclaw" {
		t.Fatalf("registry %+v", registry)
	}
}

// A missing registry is an empty registry, not a failure.
func TestMissingRegistryIsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(home, ".samcheonpo"))
	registry, err := LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Bundles) != 0 {
		t.Fatalf("registry %+v", registry)
	}
}
