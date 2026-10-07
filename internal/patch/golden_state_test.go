package patch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func gsWorkDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func gsFiles(content string) map[string]File {
	return map[string]File{
		"src/a.py":   {Data: []byte(content), Mode: 0o644},
		"bin/run.sh": {Data: []byte("#!/bin/sh\n"), Mode: 0o755},
		"docs/n.md":  {Data: []byte("n\n")},
		"pkg/deep/x": {Data: []byte("x")},
	}
}

func gsCapture(t *testing.T, g *GoldenStateManager, reason, content string) string {
	t.Helper()
	id, err := g.Capture(SnapshotMeta{Reason: reason, Session: "s1", Seq: 7, Tree: "git:abc"}, gsFiles(content))
	if err != nil || id == "" {
		t.Fatalf("capture(%q): %q %v", reason, id, err)
	}
	return id
}

func gsSnapDir(workDir, id string) string {
	return filepath.Join(workDir, ".samcheonpo", "snapshots", id)
}

// A capture records the files byte for byte with their permission bits, and
// its metadata names the pass, the fingerprint and the content hashes.
func TestGoldenStateCaptureRecordsFilesAndMeta(t *testing.T) {
	work := gsWorkDir(t)
	g := NewGoldenStateManager(work)
	id := gsCapture(t, g, "pytest -q", "x = 2\n")
	meta, err := g.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ID != id || meta.Reason != "pytest -q" || meta.Session != "s1" || meta.Seq != 7 || meta.Tree != "git:abc" || meta.Timestamp.IsZero() {
		t.Fatalf("meta: %+v", meta)
	}
	if strings.Join(meta.Files, ",") != "bin/run.sh,docs/n.md,pkg/deep/x,src/a.py" {
		t.Fatalf("files are sorted: %v", meta.Files)
	}
	if meta.Hashes["src/a.py"] != HashContent("x = 2\n") {
		t.Fatalf("hashes name the content: %v", meta.Hashes)
	}
	f, err := g.ReadFile(id, "bin/run.sh")
	if err != nil || string(f.Data) != "#!/bin/sh\n" || f.Mode != 0o755 {
		t.Fatalf("read: %+v %v", f, err)
	}
	if f, err := g.ReadFile(id, "docs/n.md"); err != nil || f.Mode != 0o644 {
		t.Fatalf("a file without a mode is stored as 0644: %+v %v", f, err)
	}
	if _, err := g.ReadFile(id, "src/other.py"); !errors.Is(err, ErrSnapshotGone) {
		t.Fatalf("a path the snapshot never held: %v", err)
	}
	if g.LatestSnapshotID() != id || g.WorkDir() != work {
		t.Fatal("latest and work dir")
	}
	if _, err := os.Stat(filepath.Join(gsSnapDir(work, id), "files", "src", "a.py")); err != nil {
		t.Fatal("the capture lives inside the project's .samcheonpo directory")
	}
}

func TestGoldenStateKeepsOnlyTheFiveMostRecentSnapshots(t *testing.T) {
	g := NewGoldenStateManager(gsWorkDir(t))
	var ids []string
	for i := 0; i < 7; i++ {
		ids = append(ids, gsCapture(t, g, "check "+string(rune('1'+i)), "c"))
	}
	list := g.ListSnapshots()
	if len(list) != 5 {
		t.Fatalf("kept %d snapshots, want 5", len(list))
	}
	for i, want := range []string{"check 7", "check 6", "check 5", "check 4", "check 3"} {
		if list[i].Reason != want || list[i].ID != ids[6-i] {
			t.Fatalf("snapshot %d = %+v, want %s (newest first)", i, list[i], want)
		}
	}
	for _, gone := range ids[:2] {
		if _, err := g.Get(gone); !errors.Is(err, ErrSnapshotGone) {
			t.Fatalf("a trimmed snapshot %s: %v", gone, err)
		}
	}
}

func TestGoldenStateRejectsUnsafeIDsAndPaths(t *testing.T) {
	work := gsWorkDir(t)
	g := NewGoldenStateManager(work)
	for _, id := range []string{"", ".", "..", "../x", "a/../../b", ".tmp-x", "nope"} {
		if _, err := g.Get(id); !errors.Is(err, ErrSnapshotGone) {
			t.Errorf("id %q: %v", id, err)
		}
	}
	for _, rel := range []string{"", "../escape", "/abs"} {
		if _, err := g.Capture(SnapshotMeta{}, map[string]File{rel: {Data: []byte("x")}}); err == nil {
			t.Errorf("path %q was captured", rel)
		}
	}
	if _, err := g.Capture(SnapshotMeta{}, map[string]File{"big": {Data: make([]byte, maxSnapshotBytes+1)}}); !errors.Is(err, ErrSnapshotTooLarge) {
		t.Fatalf("an oversized capture is refused whole: %v", err)
	}
	if list := g.ListSnapshots(); len(list) != 0 {
		t.Fatalf("refused captures leave nothing behind: %v", list)
	}
}

// A capture that died halfway, or a stray directory, is not a snapshot.
func TestGoldenStateIgnoresPartialSnapshotDirectories(t *testing.T) {
	work := gsWorkDir(t)
	g := NewGoldenStateManager(work)
	id := gsCapture(t, g, "ok", "c")
	for _, d := range []string{".tmp-20260101T000000.000000000Z-dead", "stray"} {
		if err := os.MkdirAll(filepath.Join(work, ".samcheonpo", "snapshots", d, "files"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	list := g.ListSnapshots()
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("list = %v", list)
	}
}

func TestGoldenStateConcurrentCaptureAndRead(t *testing.T) {
	g := NewGoldenStateManager(gsWorkDir(t))
	gsCapture(t, g, "first", "c")
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	ids := make(chan string, 64)
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			id, err := g.Capture(SnapshotMeta{Reason: "concurrent"}, gsFiles("c"))
			if err != nil {
				errs <- err
				return
			}
			ids <- id
		}()
		go func() {
			defer wg.Done()
			if id := g.LatestSnapshotID(); id != "" {
				if _, err := g.ReadFile(id, "src/a.py"); err != nil && !errors.Is(err, ErrSnapshotGone) {
					errs <- err
				}
			}
			_ = g.ListSnapshots()
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Fatalf("concurrent snapshot operation: %v", err)
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("snapshot id %q was handed out twice", id)
		}
		seen[id] = true
	}
	if list := g.ListSnapshots(); len(list) > 5 {
		t.Fatalf("kept %d snapshots, want at most 5", len(list))
	}
}

func TestGoldenStateCaptureFailsLoudlyOnAMissingWorkDir(t *testing.T) {
	g := NewGoldenStateManager(filepath.Join(gsWorkDir(t), "missing"))
	if _, err := g.Capture(SnapshotMeta{}, gsFiles("c")); err == nil {
		t.Fatal("a missing work dir must fail the capture")
	}
	if g.LatestSnapshotID() != "" || g.ListSnapshots() != nil {
		t.Fatal("a missing work dir has no snapshots")
	}
}
