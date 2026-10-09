package live

import (
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testutil/fakeexe"
)

func TestRunGroupDoesNotWaitForOrphanHoldingThePipe(t *testing.T) {
	exe := fakeexe.Install(t, t.TempDir(), "spawn", fakeexe.Spec{Behavior: "spawner", Option: "orphan"})
	start := time.Now()
	code, out, timedOut, _ := runGroup(t.TempDir(), `"`+exe+`"`, 20*time.Second)
	if code != 0 || timedOut {
		t.Fatalf("code=%d timedOut=%v output=%q", code, timedOut, out)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("the check waited %v for a descendant", time.Since(start))
	}
}
