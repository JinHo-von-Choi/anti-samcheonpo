package bench

import (
	"os"
	"path/filepath"
	"testing"
)

// writeGrader writes a small grader with one case.
func writeGrader(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := `{"language":"py","cases":[{"input":[1,2,3],"want":"the-expected-answer-value"}]}`
	if err := os.WriteFile(filepath.Join(dir, "cases.json"), []byte(cases), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "verify.py"), []byte("print('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeRepo writes a small repository copy.
func writeRepo(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A trial that never reached the grader is clean.
func TestContaminationIsCleanWhenTheGraderWasNotReached(t *testing.T) {
	base := t.TempDir()
	grader := writeGrader(t, filepath.Join(base, "grader"))
	repo := writeRepo(t, filepath.Join(base, "repo"), map[string]string{"solution.py": "def solve(x):\n    return 1\n"})
	c := DetectContamination(grader, repo, []string{filepath.Join(repo, "solution.py")})
	if c.Verdict != ContaminationClean {
		t.Fatalf("verdict %q: %+v", c.Verdict, c)
	}
}

// A tool call that reached the grader tree is a touch, and the trial says
// so even when the agent changed nothing.
func TestContaminationNoticesAReachedGrader(t *testing.T) {
	base := t.TempDir()
	grader := writeGrader(t, filepath.Join(base, "grader"))
	repo := writeRepo(t, filepath.Join(base, "repo"), map[string]string{"solution.py": "def solve(x):\n    return 1\n"})
	c := DetectContamination(grader, repo, []string{filepath.Join(grader, "cases.json")})
	if c.Verdict != ContaminationTouched {
		t.Fatalf("verdict %q: %+v", c.Verdict, c)
	}
	if len(c.Touched) != 1 {
		t.Fatalf("touched %+v", c.Touched)
	}
}

// A grader file copied into the repository is contamination even when the
// copy was renamed.
func TestContaminationNoticesACopiedGraderFile(t *testing.T) {
	base := t.TempDir()
	grader := writeGrader(t, filepath.Join(base, "grader"))
	repo := writeRepo(t, filepath.Join(base, "repo"), map[string]string{"solution.py": "def solve(x):\n    return 1\n"})
	body, err := os.ReadFile(filepath.Join(grader, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "data.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	c := DetectContamination(grader, repo, nil)
	if c.Verdict != ContaminationCopied {
		t.Fatalf("verdict %q: %+v", c.Verdict, c)
	}
	if len(c.Copied) != 1 || c.Copied[0] != "data.json" {
		t.Fatalf("copied %+v", c.Copied)
	}
}

// An expected answer that appears in the produced repository is a leak.
func TestContaminationNoticesALeakedAnswer(t *testing.T) {
	base := t.TempDir()
	grader := writeGrader(t, filepath.Join(base, "grader"))
	repo := writeRepo(t, filepath.Join(base, "repo"), map[string]string{
		"solution.py": "def solve(x):\n    return 'the-expected-answer-value'\n"})
	c := DetectContamination(grader, repo, nil)
	if c.Verdict != ContaminationLeaked {
		t.Fatalf("verdict %q: %+v", c.Verdict, c)
	}
	if len(c.Leaked) != 1 || c.Leaked[0] != "solution.py" {
		t.Fatalf("leaked %+v", c.Leaked)
	}
}

// A short answer appears in ordinary code by accident, so it is not
// looked for. Looking for it would report a leak that is not one.
func TestContaminationIgnoresShortAnswers(t *testing.T) {
	base := t.TempDir()
	grader := filepath.Join(base, "grader")
	if err := os.MkdirAll(grader, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := `{"language":"py","cases":[{"input":[1,2,3],"want":42}]}`
	if err := os.WriteFile(filepath.Join(grader, "cases.json"), []byte(cases), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := writeRepo(t, filepath.Join(base, "repo"), map[string]string{"solution.py": "def solve(x):\n    return 42\n"})
	c := DetectContamination(grader, repo, nil)
	if c.Verdict != ContaminationClean {
		t.Fatalf("verdict %q: %+v", c.Verdict, c)
	}
}

// The grader tree is the path the monitor has to stop, whole, so a file
// added to the grader later is covered too.
func TestProtectedPathsCoverTheWholeGraderTree(t *testing.T) {
	if got := protectedPaths(""); got != nil {
		t.Fatalf("protected %+v", got)
	}
	got := protectedPaths("/tmp/grad")
	if len(got) != 1 || got[0] != "/tmp/grad" {
		t.Fatalf("protected %+v", got)
	}
}
