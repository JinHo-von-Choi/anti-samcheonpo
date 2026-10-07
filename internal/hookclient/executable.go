package hookclient

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Version is set alongside the main CLI version by release builds.
var Version = "0.2.0"

type versionBytes struct{ bytes.Buffer }

func (b *versionBytes) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 256 - b.Len(); remaining > 0 {
		_, _ = b.Buffer.Write(p[:min(len(p), remaining)])
	}
	return n, nil
}

func TransportVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.WaitDelay = 50 * time.Millisecond
	var output versionBytes
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(output.String())
}

// Executable chooses the matching sibling transport when installed. The main
// binary remains a supported fallback for existing single-binary installs.
func Executable(main string) string {
	p := filepath.Join(filepath.Dir(main), "samcheonpo-hook")
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0111 != 0 {
		return p
	}
	return main
}

func daemonExecutable(exe string) string {
	if filepath.Base(exe) == "samcheonpo-hook" {
		return filepath.Join(filepath.Dir(exe), "samcheonpo")
	}
	return exe
}
