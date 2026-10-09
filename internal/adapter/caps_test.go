package adapter

import (
	"os"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testutil/fakeexe"
)

func TestVersionProbeBoundsTimeAndOutput(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	fakeexe.Install(t, bin, "claude", fakeexe.Spec{SleepMS: 5000})
	defer func(d time.Duration) { probeTimeout = d }(probeTimeout)
	probeTimeout = 300 * time.Millisecond
	start := time.Now()
	if got := ProbeVersion("claude"); got != "" {
		t.Fatal(got)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("version process exceeded bounded probe")
	}
	var out versionOutput
	if n, err := out.Write(make([]byte, 1<<20)); n != 1<<20 || err != nil || len(out.data) != 4096 {
		t.Fatal("unbounded version output")
	}
	if got := ProbeVersion("/untrusted/program"); got != "" {
		t.Fatal("unknown agent executed")
	}
}
