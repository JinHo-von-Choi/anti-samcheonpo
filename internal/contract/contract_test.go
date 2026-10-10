package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const good = `goal: 로그인 토큰이 만료되면 재로그인 화면으로 보낸다
done:
  - check: "npm test -- auth"
  - manual: 만료된 토큰으로 접속하면 /login으로 이동한다
scope:
  allow: ["src/auth/**", "src/pages/login/**"]
  protect: ["tests/**", ".env*"]
budget: {krw: 5000, minutes: 40, same_error_retries: 5}
forbid: ["테스트 수정", "새 의존성 추가"]
`

func TestConcurrentAcceptanceWrites(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- SaveAcceptance(dir, Acceptance{State: StateAccepted, ChecksHash: "check"})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("atomic state write failed: %v", err)
		}
	}
	if got := LoadAcceptance(dir); got.State != StateAccepted || got.ChecksHash != "check" {
		t.Fatalf("invalid state: %+v", got)
	}
}

func TestParseValid(t *testing.T) {
	c, errs := Parse([]byte(good))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(c.MachineChecks()) != 1 || c.Done[0].ID != "c1" || c.Done[1].ID != "c2" {
		t.Errorf("checks %+v", c.Done)
	}
	if !c.InScope("src/auth/token.ts") || c.InScope("src/theme/dark.css") || c.InScope("") {
		t.Error("scope matching")
	}
	if !c.InScope(".samcheonpo/contract.yml") {
		t.Error("contract files are always in scope")
	}
	if !c.Protected("tests/auth.test.ts") || !c.Protected(".env.local") || c.Protected("src/a.ts") {
		t.Error("protect matching")
	}
	if !c.ForbidsTestChanges() {
		t.Error("forbid detection")
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"missing goal":  "done:\n  - check: x\n",
		"no done":       "goal: x\n",
		"both":          "goal: x\ndone:\n  - check: a\n    manual: b\n",
		"bad expect":    "goal: x\ndone:\n  - check: a\n    expect: exit1\n",
		"bad isolation": "goal: x\ndone:\n  - check: a\n    isolation: docker\n",
		"unknown field": "goal: x\ndone:\n  - check: a\ncolour: red\n",
		"bad glob":      "goal: x\ndone:\n  - check: a\nscope:\n  allow: [\"src/[\"]\n",
		"negative":      "goal: x\ndone:\n  - check: a\nbudget: {krw: -1}\n",
	}
	for name, src := range cases {
		if _, errs := Parse([]byte(src)); len(errs) == 0 {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	root := "/w/p"
	if NormalizePath(root, "/w/p/src/a.go") != "src/a.go" || NormalizePath(root, "src/../b.go") != "b.go" {
		t.Error("normalization")
	}
	if NormalizePath(root, "../x") != "" || NormalizePath(root, "/etc/passwd") != "" {
		t.Error("paths outside the root are always out of scope")
	}
}

func TestAcceptanceLifecycle(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(dir), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	c, raw, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st := CurrentState(dir, c, raw); st.State != StateDraft {
		t.Errorf("new contract state %s", st.State)
	}
	if _, err := Accept(dir, c, raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	if st := CurrentState(dir, c, raw); st.State != StateAccepted {
		t.Errorf("after accept %s", st.State)
	}
	// changing a check after acceptance requires re-acceptance
	changed := strings.Replace(good, "npm test -- auth", "npm test", 1)
	if err := os.WriteFile(Path(dir), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	c2, raw2, _ := Load(dir)
	if st := CurrentState(dir, c2, raw2); st.State != StateStale {
		t.Errorf("changed checks must be stale, got %s", st.State)
	}
	// the goal is part of what the user accepted
	goalOnly := strings.Replace(good, "로그인 토큰이", "토큰이", 1)
	_ = os.WriteFile(Path(dir), []byte(goalOnly), 0o644)
	c3, raw3, _ := Load(dir)
	if st := CurrentState(dir, c3, raw3); st.State != StateStale {
		t.Errorf("goal edit must need re-acceptance, got %s", st.State)
	}
}

func TestAcceptanceCoversEveryAuthorityField(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	edits := map[string][2]string{
		"scope":   {`allow: ["src/auth/**", "src/pages/login/**"]`, `allow: ["**"]`},
		"protect": {`protect: ["tests/**", ".env*"]`, `protect: []`},
		"budget":  {"krw: 5000", "krw: 500000"},
		"forbid":  {`forbid: ["테스트 수정", "새 의존성 추가"]`, `forbid: []`},
	}
	for name, e := range edits {
		dir := t.TempDir()
		_ = os.MkdirAll(Dir(dir), 0o755)
		_ = os.WriteFile(Path(dir), []byte(good), 0o644)
		c, raw, _ := Load(dir)
		if _, err := Accept(dir, c, raw, time.Now()); err != nil {
			t.Fatal(err)
		}
		changed := strings.Replace(good, e[0], e[1], 1)
		if changed == good {
			t.Fatalf("%s: edit did not apply", name)
		}
		_ = os.WriteFile(Path(dir), []byte(changed), 0o644)
		c2, raw2, _ := Load(dir)
		if st := CurrentState(dir, c2, raw2); st.State != StateStale {
			t.Errorf("%s change kept acceptance: %s", name, st.State)
		}
	}
}

func TestForgedOrCopiedAcceptanceGrantsNothing(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	dir := t.TempDir()
	_ = os.MkdirAll(Dir(dir), 0o755)
	_ = os.WriteFile(Path(dir), []byte(good), 0o644)
	c, raw, _ := Load(dir)

	// an agent writes a plausible state file without the user's key
	forged := Acceptance{State: StateAccepted, ChecksHash: c.ChecksHash(), AuthorityHash: AuthorityDigest(c), AcceptedAt: time.Now()}
	b, _ := json.Marshal(forged)
	_ = os.WriteFile(filepath.Join(Dir(dir), "contract.state.json"), b, 0o644)
	if st := CurrentState(dir, c, raw); st.State != StateStale {
		t.Fatalf("forged acceptance honored: %s", st.State)
	}

	// a legacy acceptance without an authority hash needs re-confirmation
	if _, err := Accept(dir, c, raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	legacy := LoadAcceptance(dir)
	legacy.AuthorityHash = ""
	if err := SaveAcceptance(dir, legacy); err != nil {
		t.Fatal(err)
	}
	if st := CurrentState(dir, c, raw); st.State != StateStale {
		t.Fatalf("legacy acceptance honored: %s", st.State)
	}

	// a signed state copied into another project grants nothing there
	if _, err := Accept(dir, c, raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	_ = os.MkdirAll(Dir(other), 0o755)
	_ = os.WriteFile(Path(other), []byte(good), 0o644)
	signed, _ := os.ReadFile(filepath.Join(Dir(dir), "contract.state.json"))
	_ = os.WriteFile(filepath.Join(Dir(other), "contract.state.json"), signed, 0o644)
	c2, raw2, _ := Load(other)
	if st := CurrentState(other, c2, raw2); st.State != StateStale {
		t.Fatalf("copied acceptance honored: %s", st.State)
	}
	if st := CurrentState(dir, c, raw); st.State != StateAccepted {
		t.Fatalf("genuine acceptance lost: %s", st.State)
	}
}

func TestCandidates(t *testing.T) {
	dir := t.TempDir()
	w := func(p, s string) {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("package.json", `{"scripts":{"test":"vitest","build":"tsc","lint":"eslint .","dev":"vite"}}`)
	w("pnpm-lock.yaml", "")
	w("go.mod", "module x\n")
	w("pyproject.toml", "[tool.pytest.ini_options]\n[tool.ruff]\n")
	w("Makefile", "test:\n\tgo test ./...\nclean:\n\trm -rf x\n")
	w(".github/workflows/ci.yml", "jobs:\n  t:\n    steps:\n      - run: npm run test:ci\n      - run: echo ${{ secrets.X }} test\n")
	got := map[string]string{}
	for _, c := range Candidates(dir) {
		got[c.Command] = c.Kind
	}
	for cmd, kind := range map[string]string{"pnpm run test": "test", "pnpm run build": "build", "pnpm run lint": "lint",
		"go test ./...": "test", "pytest -q": "test", "ruff check .": "lint", "make test": "test", "npm run test:ci": "test"} {
		if got[cmd] != kind {
			t.Errorf("candidate %q kind %q, want %q (all: %v)", cmd, got[cmd], kind, got)
		}
	}
	if _, ok := got["pnpm run dev"]; ok {
		t.Error("dev script is not a check")
	}
	for cmd := range got {
		if strings.Contains(cmd, "${{") {
			t.Error("CI expressions must be skipped")
		}
	}
	d := DraftRequest(Candidates(dir), "x")
	if !strings.Contains(d, "go test ./...") || !strings.Contains(d, ".samcheonpo/contract.yml") {
		t.Error("draft request must carry candidates and the contract path")
	}
}

func TestFileHashIgnoresLineEndings(t *testing.T) {
	lf := []byte("goal: x\ndone:\n  - check: \"true\"\n")
	crlf := []byte("goal: x\r\ndone:\r\n  - check: \"true\"\r\n")
	if fileHash(lf) != fileHash(crlf) {
		t.Fatal("the same contract with different line endings must hash alike")
	}
	if fileHash(lf) == fileHash([]byte("goal: y\n")) {
		t.Fatal("different contracts must not hash alike")
	}
}

func TestCurrentStateWithMissingSigningKeyIsReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	root := t.TempDir()
	if err := os.MkdirAll(Dir(root), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(root), []byte(good), 0600); err != nil {
		t.Fatal(err)
	}
	c, raw, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Accept(root, c, raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(acceptanceKeyPath()); err != nil {
		t.Fatal(err)
	}
	if st := CurrentState(root, c, raw); st.State == StateAccepted {
		t.Fatal("missing key kept trusted acceptance")
	}
	if _, err = os.Stat(acceptanceKeyPath()); !os.IsNotExist(err) {
		t.Fatalf("diagnosis created key: %v", err)
	}
}
