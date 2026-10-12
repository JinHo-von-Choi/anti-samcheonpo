package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOversizedLabelCannotSilentlyDropReviewEvidence(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "human")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(strings.Repeat("x", (1<<20)+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if labels, err := LoadLabels(root); err == nil {
		t.Fatalf("oversized label silently disappeared: %+v", labels)
	}
}
