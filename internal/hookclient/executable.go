package hookclient

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Version is set alongside the main CLI version by release builds.
var Version = "0.7.0"

type versionBytes struct{ bytes.Buffer }

func (b *versionBytes) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 256 - b.Len(); remaining > 0 {
		_, _ = b.Buffer.Write(p[:min(len(p), remaining)])
	}
	return n, nil
}

// versionTimeout bounds how long the helper may take to report its version.
var versionTimeout = 500 * time.Millisecond

func TransportVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
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

// ExeSuffix is the file name suffix of a program on this system.
var ExeSuffix = func() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}()

// Executable chooses the matching sibling transport when installed. The main
// binary remains a supported fallback for existing single-binary installs.
// Windows has no execute permission bit, so a regular file with the program
// suffix counts.
func Executable(main string) string {
	p := filepath.Join(filepath.Dir(main), "samcheonpo-hook"+ExeSuffix)
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && (runtime.GOOS == "windows" || st.Mode().Perm()&0111 != 0) {
		return p
	}
	return main
}

func daemonExecutable(exe string) string {
	if strings.TrimSuffix(strings.ToLower(filepath.Base(exe)), ".exe") == "samcheonpo-hook" {
		return filepath.Join(filepath.Dir(exe), "samcheonpo"+ExeSuffix)
	}
	return exe
}
