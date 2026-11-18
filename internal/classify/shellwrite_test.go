package classify

import (
	"slices"
	"testing"
)

func TestShellWriteTargets(t *testing.T) {
	cases := []struct {
		cmd     string
		paths   []string
		patches []string
	}{
		{`sed -i 's/want/got/' internal/x_test.go`, []string{"internal/x_test.go"}, nil},
		{`sed -i.bak -e 's/a/b/' a_test.py b_test.py`, []string{"a_test.py", "b_test.py"}, nil},
		{`sed -Ei 's/a/b/' tests/t.py && go test ./...`, []string{"tests/t.py"}, nil},
		{`sed 's/a/b/' x_test.go`, nil, nil},
		{`perl -pi -e 's/1/2/' t/x_test.go`, []string{"t/x_test.go"}, nil},
		{`echo ok > tests/out.txt 2>&1`, []string{"tests/out.txt"}, nil},
		{`go test ./... 2>/dev/null`, nil, nil},
		{`printf x | tee -a tests/a.py`, []string{"tests/a.py"}, nil},
		{`cp fixture.py tests/`, []string{"tests/", "tests/fixture.py"}, nil},
		{`cp x_test.go /tmp/`, []string{"/tmp/", "/tmp/x_test.go"}, nil},
		{`mv tests/a_test.go tests/b_test.go`, []string{"tests/b_test.go", "tests/b_test.go/a_test.go", "tests/a_test.go"}, nil},
		{`rm -f tests/a_test.go`, []string{"tests/a_test.go"}, nil},
		{`truncate -s 0 tests/a_test.go`, []string{"tests/a_test.go"}, nil},
		{`dd if=/dev/zero of=tests/a_test.go bs=1 count=0`, []string{"tests/a_test.go"}, nil},
		{`git checkout -- tests/a_test.go`, []string{"tests/a_test.go"}, nil},
		{`git checkout main`, nil, nil},
		{`git -C . restore tests/a_test.go`, []string{"tests/a_test.go"}, nil},
		{`git apply fix.patch`, nil, []string{"fix.patch"}},
		{`patch -p1 < fix.diff`, nil, []string{"fix.diff"}},
		{`python3 -c "open('tests/a_test.py','w').write('')"`, []string{"tests/a_test.py"}, nil},
		{`node -e "require('fs').writeFileSync('a.test.js', '')"`, []string{"a.test.js"}, nil},
		{"cat > tests/a_test.go <<'EOF'\npackage x\nEOF", []string{"tests/a_test.go"}, nil},
		{"git apply <<'EOF'\n--- a/tests/a_test.go\n+++ b/tests/a_test.go\n@@\nEOF", []string{"tests/a_test.go"}, nil},
		{`echo x > "$OUT"`, nil, nil},
		{`cat tests/a_test.go`, nil, nil},
		{`grep -r assert tests/`, nil, nil},
		{`FOO=1 sudo sed -i s/a/b/ t_test.go`, []string{"t_test.go"}, nil},
	}
	for _, c := range cases {
		w := ShellWriteTargets(c.cmd)
		if !slices.Equal(w.Paths, c.paths) || !slices.Equal(w.PatchFiles, c.patches) {
			t.Errorf("%q: paths %q patches %q, want %q %q", c.cmd, w.Paths, w.PatchFiles, c.paths, c.patches)
		}
	}
}

func TestPatchTargets(t *testing.T) {
	body := "diff --git a/x_test.go b/x_test.go\n--- a/x_test.go\n+++ b/x_test.go\n@@ -1 +1 @@\n--- /dev/null\n+++ b/new_test.go\t2026-01-01\n"
	if got := PatchTargets(body); !slices.Equal(got, []string{"x_test.go", "new_test.go"}) {
		t.Fatalf("got %q", got)
	}
}
