package patch

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func gsWorkDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func gsWrite(t *testing.T, workDir, rel, content string) {
	t.Helper()
	abs := filepath.Join(workDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gsRead(t *testing.T, workDir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(workDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func gsCapture(t *testing.T, g *GoldenStateManager, reason string) string {
	t.Helper()
	id, err := g.CapturePassState(reason)
	if err != nil {
		t.Fatalf("capture(%q): %v", reason, err)
	}
	if id == "" {
		t.Fatalf("capture(%q) returned no snapshot id", reason)
	}
	return id
}

func gsSnapDir(workDir, id string) string {
	return filepath.Join(workDir, ".samcheonpo", "snapshots", id)
}

// A check that passed is the only point worth returning to: the tree is put
// back byte for byte, including a file the agent deleted in the meantime.
func TestGoldenStateRestoresTheTreeAsItWasAtThePassingCheck(t *testing.T) {
	work := gsWorkDir(t)
	gsWrite(t, work, "a.txt", "original a\n")
	gsWrite(t, work, "src/b.txt", "original b\n")
	gsWrite(t, work, "deep/nested/c.txt", "original c\n")

	g := NewGoldenStateManager(work)
	gsCapture(t, g, "all checks passed")

	// the agent keeps working and leaves the tree in a mess
	gsWrite(t, work, "a.txt", "broken a\n")
	gsWrite(t, work, "deep/nested/c.txt", "half written")
	if err := os.Remove(filepath.Join(work, "src", "b.txt")); err != nil {
		t.Fatal(err)
	}
	gsWrite(t, work, "unrelated.txt", "written after the snapshot\n")

	if err := g.RestoreState(g.LatestSnapshotID()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for rel, want := range map[string]string{
		"a.txt":             "original a\n",
		"src/b.txt":         "original b\n",
		"deep/nested/c.txt": "original c\n",
		"unrelated.txt":     "written after the snapshot\n",
	} {
		if got := gsRead(t, work, rel); got != want {
			t.Fatalf("%s after rollback = %q, want %q", rel, got, want)
		}
	}

	// restoring twice must not change anything further
	if err := g.RestoreState(g.LatestSnapshotID()); err != nil {
		t.Fatalf("second restore: %v", err)
	}
	if got := gsRead(t, work, "a.txt"); got != "original a\n" {
		t.Fatalf("second restore changed a.txt to %q", got)
	}
}

func TestGoldenStateKeepsTheFilePermissionsOfTheCapture(t *testing.T) {
	work := gsWorkDir(t)
	abs := filepath.Join(work, "script.sh")
	if err := os.WriteFile(abs, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	g := NewGoldenStateManager(work)
	id := gsCapture(t, g, "checks passed")

	if err := os.Chmod(abs, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := g.RestoreState(id); err != nil {
		t.Fatalf("restore: %v", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o755 {
		t.Fatalf("restored mode = %v, want 0755", st.Mode().Perm())
	}
}

func TestGoldenStateMetaRecordsTheReasonTimeAndFiles(t *testing.T) {
	work := gsWorkDir(t)
	gsWrite(t, work, "src/a.go", "package a\n")
	gsWrite(t, work, "README.md", "readme\n")
	g := NewGoldenStateManager(work)

	before := time.Now().UTC()
	id := gsCapture(t, g, "go test passed")
	after := time.Now().UTC()

	list := g.ListSnapshots()
	if len(list) != 1 {
		t.Fatalf("ListSnapshots() = %d snapshots, want 1", len(list))
	}
	meta := list[0]
	if meta.ID != id {
		t.Fatalf("meta ID = %q, want %q", meta.ID, id)
	}
	if meta.Reason != "go test passed" {
		t.Fatalf("meta reason = %q", meta.Reason)
	}
	if meta.Timestamp.Before(before) || meta.Timestamp.After(after) {
		t.Fatalf("meta timestamp %v is outside [%v, %v]", meta.Timestamp, before, after)
	}
	want := []string{"README.md", "src/a.go"}
	if !slices.Equal(meta.Files, want) {
		t.Fatalf("meta files = %v, want %v", meta.Files, want)
	}
	if got := g.LatestSnapshotID(); got != id {
		t.Fatalf("LatestSnapshotID() = %q, want %q", got, id)
	}
	if _, err := os.Stat(filepath.Join(gsSnapDir(work, id), "meta.json")); err != nil {
		t.Fatalf("snapshot metadata file: %v", err)
	}
}

func TestGoldenStateLeavesHeavyAndInternalDirsOut(t *testing.T) {
	work := gsWorkDir(t)
	gsWrite(t, work, "src/a.go", "package a\n")
	gsWrite(t, work, ".git/config", "[core]\n")
	gsWrite(t, work, "node_modules/dep/index.js", "module.exports = 1\n")
	gsWrite(t, work, "dist/bundle.js", "bundled\n")
	gsWrite(t, work, ".samcheonpo/contract.yml", "scope: all\n")
	gsWrite(t, work, ".venv/lib/mod.py", "mod = 1\n")

	g := NewGoldenStateManager(work)
	id := gsCapture(t, g, "checks passed")

	meta := g.ListSnapshots()[0]
	if !slices.Equal(meta.Files, []string{"src/a.go"}) {
		t.Fatalf("meta files = %v, want only src/a.go", meta.Files)
	}
	for _, dir := range []string{".git", "node_modules", "dist", ".venv"} {
		if _, err := os.Stat(filepath.Join(gsSnapDir(work, id), "files", dir)); !os.IsNotExist(err) {
			t.Fatalf("%s was copied into the snapshot", dir)
		}
	}
	// the tool's own state directory is never swept into a snapshot
	if _, err := os.Stat(filepath.Join(gsSnapDir(work, id), "files", ".samcheonpo")); !os.IsNotExist(err) {
		t.Fatal(".samcheonpo was copied into the snapshot")
	}

	// an excluded file is neither captured nor touched by a rollback
	gsWrite(t, work, ".git/config", "[core]\n\tchanged\n")
	gsWrite(t, work, "src/a.go", "package a // edited\n")
	if err := g.RestoreState(id); err != nil {
		t.Fatal(err)
	}
	if got := gsRead(t, work, "src/a.go"); got != "package a\n" {
		t.Fatalf("src/a.go after rollback = %q", got)
	}
	if got := gsRead(t, work, ".git/config"); got != "[core]\n\tchanged\n" {
		t.Fatalf("an excluded file was rewritten by a rollback: %q", got)
	}
}

func TestGoldenStateSkipsLinksAndIrregularFiles(t *testing.T) {
	work := gsWorkDir(t)
	gsWrite(t, work, "real.txt", "real\n")
	gsWrite(t, work, "dir/inner.txt", "inner\n")
	if err := os.Symlink("real.txt", filepath.Join(work, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("dir", filepath.Join(work, "dirlink")); err != nil {
		t.Fatal(err)
	}

	g := NewGoldenStateManager(work)
	id := gsCapture(t, g, "checks passed")
	meta := g.ListSnapshots()[0]
	if !slices.Equal(meta.Files, []string{"dir/inner.txt", "real.txt"}) {
		t.Fatalf("meta files = %v, want the regular files only", meta.Files)
	}
	// a link swapped for a regular file must not be written through
	_ = os.Remove(filepath.Join(work, "link.txt"))
	gsWrite(t, work, "link.txt", "now a plain file\n")
	if err := g.RestoreState(id); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := gsRead(t, work, "link.txt"); got != "now a plain file\n" {
		t.Fatalf("a link was captured and written through: %q", got)
	}
}

func TestGoldenStateKeepsOnlyTheFiveMostRecentSnapshots(t *testing.T) {
	work := gsWorkDir(t)
	gsWrite(t, work, "a.txt", "content\n")
	g := NewGoldenStateManager(work)

	var ids []string
	for i := 0; i < 7; i++ {
		ids = append(ids, gsCapture(t, g, "check "+string(rune('1'+i))))
	}

	list := g.ListSnapshots()
	if len(list) != 5 {
		t.Fatalf("kept %d snapshots, want 5", len(list))
	}
	wantReasons := []string{"check 7", "check 6", "check 5", "check 4", "check 3"}
	for i, want := range wantReasons {
		if list[i].Reason != want {
			t.Fatalf("snapshot %d reason = %q, want %q (newest first)", i, list[i].Reason, want)
		}
		if list[i].ID != ids[7-1-i] {
			t.Fatalf("snapshot %d id = %q, want %q", i, list[i].ID, ids[7-1-i])
		}
	}
	if got := g.LatestSnapshotID(); got != ids[6] {
		t.Fatalf("LatestSnapshotID() = %q, want %q", got, ids[6])
	}
	for _, gone := range ids[:2] {
		if _, err := os.Stat(gsSnapDir(work, gone)); !os.IsNotExist(err) {
			t.Fatalf("trimmed snapshot %s is still on disk", gone)
		}
		if err := g.RestoreState(gone); !errors.Is(err, ErrSnapshotGone) {
			t.Fatalf("restoring a trimmed snapshot %s gave %v, want ErrSnapshotGone", gone, err)
		}
	}
	if err := g.RestoreState(ids[6]); err != nil {
		t.Fatalf("a kept snapshot cannot be restored: %v", err)
	}
}

func TestGoldenStateRestoreRejectsUnknownOrUnsafeIDs(t *testing.T) {
	work := gsWorkDir(t)
	g := NewGoldenStateManager(work)
	gsCapture(t, g, "checks passed")

	for _, id := range []string{"", "no-such-snapshot", "../../etc", "..", "a/b", ".tmp-half"} {
		if err := g.RestoreState(id); !errors.Is(err, ErrSnapshotGone) {
			t.Fatalf("RestoreState(%q) gave %v, want ErrSnapshotGone", id, err)
		}
	}
	if got := g.LatestSnapshotID(); got == "" {
		t.Fatal("a failed restore removed the latest snapshot")
	}
}

func TestGoldenStateIgnoresPartialSnapshotDirectories(t *testing.T) {
	work := gsWorkDir(t)
	gsWrite(t, work, "a.txt", "content\n")
	g := NewGoldenStateManager(work)
	id := gsCapture(t, g, "checks passed")

	snaps := filepath.Join(work, ".samcheonpo", "snapshots")
	if err := os.MkdirAll(filepath.Join(snaps, ".tmp-halfwritten", "files"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(snaps, "leftover"), 0o700); err != nil {
		t.Fatal(err)
	}

	if list := g.ListSnapshots(); len(list) != 1 || list[0].ID != id {
		t.Fatalf("a half-written directory is listed as a snapshot: %+v", list)
	}
	if got := g.LatestSnapshotID(); got != id {
		t.Fatalf("LatestSnapshotID() = %q, want %q", got, id)
	}
	if err := g.RestoreState(".tmp-halfwritten"); err == nil {
		t.Fatal("a half-written directory can be restored")
	}
	if err := os.WriteFile(filepath.Join(snaps, ".tmp-halfwritten", "meta.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if list := g.ListSnapshots(); len(list) != 1 {
		t.Fatalf("a corrupt metadata file is listed as a snapshot: %+v", list)
	}
}

func TestGoldenStateDoesNotWriteThroughALinkThatLeavesTheProject(t *testing.T) {
	work := gsWorkDir(t)
	outside := gsWorkDir(t)
	gsWrite(t, outside, "keep.txt", "outside\n")
	gsWrite(t, work, "src/keep.txt", "project\n")

	g := NewGoldenStateManager(work)
	id := gsCapture(t, g, "checks passed")

	if err := os.RemoveAll(filepath.Join(work, "src")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(work, "src")); err != nil {
		t.Fatal(err)
	}
	if err := g.RestoreState(id); err == nil {
		t.Fatal("a rollback through a link out of the project succeeded")
	}
	if got := gsRead(t, outside, "keep.txt"); got != "outside\n" {
		t.Fatalf("a rollback changed a file outside the project: %q", got)
	}
}

// The manager is used from hook callbacks and the daemon at the same time, so
// capture, restore, listing and trimming all run under one lock.
func TestGoldenStateConcurrentCaptureAndRestore(t *testing.T) {
	work := gsWorkDir(t)
	for i := 0; i < 12; i++ {
		gsWrite(t, work, filepath.Join("pkg", "f"+string(rune('a'+i))+".go"), "package pkg\n")
	}
	g := NewGoldenStateManager(work)
	gsCapture(t, g, "checks passed")

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	ids := make(chan string, 64)
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			id, err := g.CapturePassState("concurrent check")
			if err != nil {
				errs <- err
				return
			}
			ids <- id
		}()
		go func() {
			defer wg.Done()
			// the newest snapshot is the one trimming never drops, so
			// concurrent captures cannot turn this into a lost snapshot
			if id := g.LatestSnapshotID(); id != "" {
				if err := g.RestoreState(id); err != nil && !errors.Is(err, ErrSnapshotGone) {
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
		t.Fatalf("kept %d snapshots after concurrent captures, want at most 5", len(list))
	}
	if err := g.RestoreState(g.LatestSnapshotID()); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("pkg", "fa.go")
	if got := gsRead(t, work, rel); got != "package pkg\n" {
		t.Fatalf("%s after the concurrent run = %q", rel, got)
	}
}

func TestGoldenStateReportsAnEmptyProjectInsteadOfFailing(t *testing.T) {
	work := gsWorkDir(t)
	g := NewGoldenStateManager(work)
	id := gsCapture(t, g, "nothing to do")

	meta := g.ListSnapshots()[0]
	if len(meta.Files) != 0 {
		t.Fatalf("an empty project captured %v", meta.Files)
	}
	if err := g.RestoreState(id); err != nil {
		t.Fatalf("restore of an empty project: %v", err)
	}
	gsWrite(t, work, "new.txt", "later\n")
	if err := g.RestoreState(id); err != nil {
		t.Fatal(err)
	}
	if got := gsRead(t, work, "new.txt"); got != "later\n" {
		t.Fatalf("a file written after an empty snapshot was removed: %q", got)
	}
}

func TestGoldenStateRestoresIntoAnEmptyDirectory(t *testing.T) {
	work := gsWorkDir(t)
	gsWrite(t, work, "src/deep/a.go", "package a\n")
	g := NewGoldenStateManager(work)
	id := gsCapture(t, g, "checks passed")

	if err := os.RemoveAll(filepath.Join(work, "src")); err != nil {
		t.Fatal(err)
	}
	if err := g.RestoreState(id); err != nil {
		t.Fatalf("restore into a removed directory: %v", err)
	}
	if got := gsRead(t, work, "src/deep/a.go"); got != "package a\n" {
		t.Fatalf("restored content = %q", got)
	}
}

func TestGoldenStateCaptureFailsLoudlyOnAMissingWorkDir(t *testing.T) {
	g := NewGoldenStateManager(filepath.Join(gsWorkDir(t), "not-there"))
	if _, err := g.CapturePassState("checks passed"); err == nil {
		t.Fatal("capturing in a directory that does not exist succeeded")
	} else if !strings.Contains(err.Error(), "not-there") {
		t.Fatalf("error does not name the work directory: %v", err)
	}
}
