package verification

import "testing"

func TestCheckTargetRelevance(t *testing.T) {
	for _, c := range []struct {
		cmd, path string
		want      bool
	}{
		{"go test ./unrelated", "auth/login.go", false},
		{"go test ./auth", "auth/login.go", true},
		{"go test ./auth", "authz/login.go", false},
		{"go test ./...", "auth/login.go", true},
		{"go test ./... -list .", "auth/login.go", false},
		{"pytest --collect-only", "auth/login.py", false},
		{"go test -run TestLogin ./auth", "auth/login.go", true},
		{"go test -run ./auth ./unrelated", "auth/login.go", false},
		{"go test ./...; true", "auth/login.go", false},
		{"echo go test ./...", "auth/login.go", false},
		{"cat auth/login.go", "auth/login.go", false},
		{"python3 login.py", "login.py", true},
		{"python3 -m pytest unrelated", "auth/login.py", false},
		{"pytest auth", "auth/login.py", true},
		{"go test ./...", "app.ts", false},
		{"npm test --workspace unrelated", "auth/login.ts", false},
	} {
		t.Run(c.cmd+"/"+c.path, func(t *testing.T) {
			if got := CoversPath(c.cmd, c.path); got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestCheckWorkingDirectory(t *testing.T) {
	if CoversPathAt("go test ./...", "auth/login.go", "/w", "/w/unrelated") {
		t.Fatal("unrelated cwd passed")
	}
	if !CoversPathAt("go test ./...", "auth/login.go", "/w", "auth") {
		t.Fatal("related cwd rejected")
	}
	if EmptyTestRun("? app [no test files]\nok app/auth 0.2s") {
		t.Fatal("mixed run lost passing tests")
	}
	if !EmptyTestRun("ok app/auth 0.2s [no tests to run]") {
		t.Fatal("empty run counted")
	}
}
