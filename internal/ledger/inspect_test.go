package ledger

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectDoesNotCreateOrMigrate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ledger.db")
	if _, err := Inspect(p); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("inspection created database")
	}
	db, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	version := db.Version()
	db.Close()
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Inspect(p); err != nil || got != version {
		t.Fatalf("%d %v", got, err)
	}
	after, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("inspection rewrote database", err)
	}
	db, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_version WHERE version>2`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if got, err := Inspect(p); err == nil || got != 2 {
		t.Fatalf("old schema migrated or hidden: %d %v", got, err)
	}
}
