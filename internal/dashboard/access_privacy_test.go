package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAccessCodeIsPrivateEvenInExistingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.code")
	code, err := AccessCode(path)
	if err != nil || len(code) != 64 {
		t.Fatalf("access code creation: %v", err)
	}
	assertPrivateDataFile(t, path)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AccessCode(path); err == nil {
		t.Fatal("existing access code overwritten")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("existing code changed")
	}
}
