package cli

import "testing"

func TestCritiqueChangePutsTestsFirst(t *testing.T) {
	diff := "diff --git a/app/x.go b/app/x.go\n--- a/app/x.go\n+++ b/app/x.go\n@@\n-a\n+b\n" +
		"diff --git a/app/x_test.go b/app/x_test.go\n--- a/app/x_test.go\n+++ b/app/x_test.go\n@@\n-c\n+d\n"
	sections := append(diffSections(diff), diffSection{path: "tests/test_new.py", text: "\n--- new file: tests/test_new.py\nx\n"})
	got := testsFirst(sections)
	want := "diff --git a/app/x_test.go b/app/x_test.go\n--- a/app/x_test.go\n+++ b/app/x_test.go\n@@\n-c\n+d\n" +
		"\n--- new file: tests/test_new.py\nx\n" +
		"diff --git a/app/x.go b/app/x.go\n--- a/app/x.go\n+++ b/app/x.go\n@@\n-a\n+b\n"
	if got != want {
		t.Fatalf("got\n%s", got)
	}
}
