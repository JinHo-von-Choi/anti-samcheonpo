package bench

import (
	"context"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPublicTaskSuiteIndependentOracles(t *testing.T) {
	for _, tool := range []string{"python3", "node"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("task suite requires %s: %v", tool, err)
		}
	}
	tasks, err := LoadLiveTasks("../../bench/live")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) < 12 || len(tasks) > 24 {
		t.Fatalf("want 12–24 tasks, got %d", len(tasks))
	}
	split, err := eval.LoadSplit("../../bench/splits/public-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(split.Groups) != len(tasks) || split.Purpose != "public_regression" {
		t.Fatal("public partition coverage or provenance changed")
	}
	languages := map[string]bool{}
	normal := 0
	for _, task := range tasks {
		if split.Groups[task.Name].Family != task.Family {
			t.Fatalf("split family mismatch: %s", task.Name)
		}
		languages[task.Language] = true
		if task.InitiallyCorrect {
			normal++
		}
		if task.Name == "fix-median" {
			continue
		} // separately covered against weakened tests and exit(0)
		t.Run(task.Name, func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), "repo")
			if err := copyTree(filepath.Join(task.Dir, "repo"), repo); err != nil {
				t.Fatal(err)
			}
			grade := func() bool {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				_, err := run(ctx, filepath.Join(task.Dir, "grader"), []string{"BENCH_REPO=" + repo, "BENCH_GRADER=" + filepath.Join(task.Dir, "grader"), "PYTHONDONTWRITEBYTECODE=1"}, "sh", "-c", task.Check)
				return err == nil
			}
			if got := grade(); got != task.InitiallyCorrect {
				t.Fatalf("initial submission pass=%v, expected %v", got, task.InitiallyCorrect)
			}
			if err := copyTree(filepath.Join("../../testdata/bench-solutions", task.Name), repo); err != nil {
				t.Fatal(err)
			}
			if !grade() {
				t.Fatal("reference solution rejected")
			}
			path := filepath.Join(repo, "app.py")
			body := "raise SystemExit(0)\n"
			if task.Language == "js" {
				path = filepath.Join(repo, "app.cjs")
				body = "process.exit(0);\n"
			}
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if grade() {
				t.Fatal("silent successful exit bypassed external oracle")
			}
		})
	}
	if len(languages) < 2 || normal < 2 {
		t.Fatal("missing language diversity or normal negative tasks")
	}
}
