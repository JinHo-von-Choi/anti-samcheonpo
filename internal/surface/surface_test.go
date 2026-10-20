package surface

import (
	"os"
	"path/filepath"
	"testing"
)

// The mapping has to describe places that exist. A path that moved is a
// mistake in the mapping, and the mapping is checked so that it cannot go
// stale quietly.
func TestSurfacePathsExist(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..")
	if missing := s.Verify(root); len(missing) > 0 {
		t.Fatalf("missing paths %v", missing)
	}
}

// Every component has to say what it is and where it lives. A component
// without a role would leave the vocabulary incomplete.
func TestSurfaceComponentsAreComplete(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]bool{}
	for _, c := range s.Components {
		if c.Name == "" || c.Role == "" || len(c.Paths) == 0 {
			t.Fatalf("component %+v", c)
		}
		if c.ExternalTerm == "" {
			t.Fatalf("component %s has no external term", c.Name)
		}
		roles[c.Role] = true
	}
	for _, role := range []string{"sensor", "guide", "editable", "external_harness"} {
		if !roles[role] {
			t.Fatalf("role %q is unused", role)
		}
	}
}

// The vocabulary the plan asks to map has to be there in full.
func TestSurfaceVocabularyCoversTheTerms(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, term := range []string{"internal_harness", "external_harness", "guide", "sensor", "editable"} {
		if s.Vocabulary[term] == "" {
			t.Fatalf("vocabulary %q is missing", term)
		}
	}
}

// The editable component is the rule pack, and only the rule pack. A
// second editable component would mean the sensor's own source is up for
// change, which is not what the project does.
func TestOnlyTheRulePackIsEditable(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var editable []string
	for _, c := range s.Components {
		if c.Role == "editable" {
			editable = append(editable, c.Name)
		}
	}
	if len(editable) != 1 || editable[0] != "editable" {
		t.Fatalf("editable components %v", editable)
	}
}

// The spec file itself is embedded, so a broken file fails the build
// rather than the reader.
func TestSpecFileIsPresent(t *testing.T) {
	if _, err := os.Stat(filepath.Join("harness-surface.json")); err != nil {
		t.Fatal(err)
	}
}
