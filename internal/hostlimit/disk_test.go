package hostlimit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOccupied(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "target", "debug"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_ = os.WriteFile(filepath.Join(root, "target", "debug", string(rune('a'+i))), make([]byte, 64<<10), 0o644)
	}
	n, err := Occupied(root, []string{"target", "missing"}, time.Now().Add(time.Second), 1000)
	if err != nil || n < 5*64<<10 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, err := Occupied(root, []string{"target"}, time.Now().Add(time.Second), 3); !errors.Is(err, ErrBudget) {
		t.Fatalf("an entry limit is reported, not passed off as a total: %v", err)
	}
	if d := SpaceOf(root); !d.Known || d.Total == 0 || d.FreeRatio() <= 0 {
		t.Fatalf("space %+v", d)
	}
}
