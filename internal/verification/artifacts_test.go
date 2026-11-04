package verification

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestArtifactsRejectMissingEmptyAndEscapingFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "empty"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"missing", "empty", ".", outside} {
		if _, err := CaptureArtifacts(context.Background(), root, []string{p}); err == nil {
			t.Fatalf("invalid artifact accepted: %s", p)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err == nil {
		if _, err := CaptureArtifacts(context.Background(), root, []string{"linked"}); err == nil {
			t.Fatal("symlink accepted")
		}
	}
}
