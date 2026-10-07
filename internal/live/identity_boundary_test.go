package live

import (
	"path/filepath"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
)

func TestSessionIdentityCannotEscapeReceiptOrCrossProject(t *testing.T) {
	s, d := reliabilitySession(t)
	for _, id := range []string{"../outside", "..", "/absolute", "x\\y", "x\ny", ""} {
		if _, err := newSession(id, "claude", s.Root, "", s.db, s.prices, adapter.ObserveOnly); err == nil {
			t.Fatalf("unsafe ID %q accepted", id)
		}
	}
	if _, err := d.session(s.ID, s.Agent, t.TempDir(), ""); err == nil {
		t.Fatal("cross-project session reuse accepted")
	}
	if got, err := d.session(s.ID, s.Agent, filepath.Join(s.Root, "."), ""); err != nil || got != s {
		t.Fatal("same normalized project rejected")
	}
}
