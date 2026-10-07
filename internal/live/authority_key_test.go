package live

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
)

func TestVerificationKeyIsBoundToAcceptedAuthority(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "input.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace(root)
	defer ws.Close()
	r := &Runner{Root: root, WS: ws}
	check := contract.Check{ID: "c", Check: "true", Pure: true, Reuse: &contract.ReuseScope{Inputs: []string{"input.txt"}, EnvironmentFiles: []string{"input.txt"}, Deterministic: true, MaxAgeSec: 60}}
	r.SetAuthority("accepted-a")
	a, err := r.CurrentKey(check, "task", 1)
	if err != nil {
		t.Fatal(err)
	}
	r.SetAuthority("accepted-b")
	b, err := r.CurrentKey(check, "task", 1)
	if err != nil {
		t.Fatal(err)
	}
	if a == b || a.AuthorityHash != "accepted-a" {
		t.Fatalf("keys must differ by authority: %+v %+v", a, b)
	}
	now := time.Now()
	e := verification.Evidence{Version: verification.Version, ID: "e", Key: a, SessionID: "s", SourceEventID: "src", ObservedAt: now, ExpiresAt: now.Add(time.Minute), Pass: true, Complete: true, ResultHash: "r"}
	if ok, _ := verification.Reusable(e, b, now.Add(time.Second)); ok {
		t.Fatal("a pass observed under another acceptance must not be reused")
	}
	if ok, why := verification.Reusable(e, a, now.Add(time.Second)); !ok {
		t.Fatalf("the same acceptance reuses its own pass: %s", why)
	}
	g := ws.Generation()
	ws.Invalidate()
	if ws.Generation() == g {
		t.Fatal("a change must advance the workspace generation")
	}
}
