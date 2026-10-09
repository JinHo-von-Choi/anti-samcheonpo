package ledger

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenPathWithCharactersThatAreSpecialInURIs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a#b c%20d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ledger.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the database was created somewhere else: %v", err)
	}
	if _, err := Inspect(path); err != nil {
		t.Fatalf("read-only inspection: %v", err)
	}
}
