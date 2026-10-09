//go:build windows

package procgroup

import (
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testutil/fakeexe"
)

// A host that wraps its children in a Job Object which forbids leaving it
// must still get a detached daemon, running inside that job.
func TestStartDetachedInsideJobThatForbidsBreakaway(t *testing.T) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job)
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		t.Fatal(err)
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		t.Skip("the test process cannot join a job:", err)
	}
	exe := fakeexe.Install(t, t.TempDir(), "daemon", fakeexe.Spec{SleepMS: 300})
	cmd := Shell(`"` + exe + `"`)
	if err := StartDetached(cmd); err != nil {
		t.Fatalf("detached start failed inside a job without breakaway: %v", err)
	}
	_, _ = cmd.Process.Wait()
}

func TestCmdShellKeepsQuotedArguments(t *testing.T) {
	exe := fakeexe.Install(t, t.TempDir(), "echoargs", fakeexe.Spec{Behavior: "argsecho"})
	out, err := cmdShell(`"` + exe + `" "a b" c "d"`).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "a b|c|d" {
		t.Fatalf("arguments arrived as %q", got)
	}
}
