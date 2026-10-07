package hookclient

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTransportVersionBoundedAndFailure(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"version", "printf '1.2.3\\n'", "1.2.3"},
		{"failure", "exit 1", ""},
		{"timeout", "sleep 2", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "helper")
			if err := os.WriteFile(path, []byte("#!/bin/sh\n"+tc.body+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
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
