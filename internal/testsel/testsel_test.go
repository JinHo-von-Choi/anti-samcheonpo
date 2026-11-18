package testsel

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolve(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n")
	write(t, root, "auth/login.go", "package auth\n")
	write(t, root, "auth/login_test.go", "package auth\n")
	write(t, root, "auth/main_test.go", "package auth\nfunc TestMain(m *testing.M) {}\n")
	write(t, root, "auth/sub/x_test.go", "package sub\n")
	write(t, root, "pkg/tests/test_a.py", "def test_a(): pass\n")
	write(t, root, "pkg/tests/conftest.py", "")
	write(t, root, "conftest.py", "")
	write(t, root, "pyproject.toml", "[tool.pytest.ini_options]\n")
	write(t, root, "web/src/sum.test.ts", "")
	write(t, root, "web/src/other.test.ts", "")
	write(t, root, "crate/Cargo.toml", "[package]\nname = \"crate\"\n")
	write(t, root, "crate/src/lib.rs", "#[cfg(test)]\nmod tests {}\n")
	write(t, root, "crate/src/plain.rs", "fn f() {}\n")
	write(t, root, "crate/tests/it.rs", "")
	write(t, root, "node_modules/dep/x.test.js", "")

	cases := []struct {
		cmd   string
		files []string
		infra []string
		known bool
	}{
		{"go test ./auth -run TestLogin", []string{"auth/login_test.go", "auth/main_test.go"}, []string{"auth/main_test.go"}, true},
		{"go test example.com/m/auth", []string{"auth/login_test.go", "auth/main_test.go"}, []string{"auth/main_test.go"}, true},
		{"go test ./auth/...", []string{"auth/login_test.go", "auth/main_test.go", "auth/sub/x_test.go"}, []string{"auth/main_test.go"}, true},
		{"pytest pkg/tests -k login", []string{"pkg/tests/test_a.py"}, []string{"conftest.py", "pkg/tests/conftest.py", "pyproject.toml"}, true},
		{"python -m pytest pkg/tests/test_a.py::test_a", []string{"pkg/tests/test_a.py"}, []string{"conftest.py", "pkg/tests/conftest.py", "pyproject.toml"}, true},
		{"npx jest sum", []string{"web/src/sum.test.ts"}, nil, true},
		{"npx jest", nil, nil, false},
		{"cargo test -p crate", []string{"crate/src/lib.rs", "crate/tests/it.rs"}, nil, true},
		{"make test", nil, nil, false},
	}
	for _, c := range cases {
		s := Resolve(root, c.cmd)
		if s.Known != c.known || !slices.Equal(s.Files, c.files) || !slices.Equal(s.Infra, c.infra) {
			t.Errorf("%q: known=%v files=%q infra=%q, want %v %q %q", c.cmd, s.Known, s.Files, s.Infra, c.known, c.files, c.infra)
		}
	}
}
