//go:build !windows

package dashboard

import (
	"os"
	"testing"
)

func assertPrivateDataFile(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("private file mode: %v %v", st, err)
	}
}
