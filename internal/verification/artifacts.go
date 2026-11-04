package verification

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type Artifact struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// CaptureArtifacts hashes bounded local regular files without storing contents.
// Fingerprint rejects symlinks, outside-root paths and concurrent mutations.
func CaptureArtifacts(ctx context.Context, root string, paths []string) ([]Artifact, error) {
	if len(paths) > 16 {
		return nil, fmt.Errorf("too many evidence artifacts")
	}
	var out []Artifact
	for _, p := range paths {
		file := p
		if !filepath.IsAbs(file) {
			file = filepath.Join(root, file)
		}
		info, err := os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return nil, fmt.Errorf("evidence artifact missing, empty or non-regular: %s", p)
		}
		digest, err := Fingerprint(ctx, root, []string{p})
		if err != nil {
			return nil, fmt.Errorf("evidence artifact %s: %w", p, err)
		}
		out = append(out, Artifact{Path: p, Digest: digest})
	}
	return out, nil
}
