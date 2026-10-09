package hookclient

import (
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testutil/fakeexe"
)

func TestTransportVersionBoundedAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec fakeexe.Spec
		want string
	}{
		{"version", fakeexe.Spec{Stdout: "1.2.3\n"}, "1.2.3"},
		{"failure", fakeexe.Spec{Exit: 1}, ""},
		{"timeout", fakeexe.Spec{SleepMS: 5000}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// the stand-in is a whole test binary, which a loaded race-enabled
			// build takes longer than the production bound to start
			defer func(d time.Duration) { versionTimeout = d }(versionTimeout)
			versionTimeout = 5 * time.Second
			if tc.name == "timeout" {
				versionTimeout = time.Second
			}
			path := fakeexe.Install(t, t.TempDir(), "helper", tc.spec)
			if got := TransportVersion(path); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	var b versionBytes
	if n, err := b.Write(make([]byte, 1024)); n != 1024 || err != nil || b.Len() != 256 {
		t.Fatal("unbounded version capture")
	}
}
