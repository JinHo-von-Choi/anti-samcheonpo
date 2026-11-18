package live

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// A core malicious pattern is refused before it runs. The agent reads the
// refusal as a failed tool call, so the reason names the rule that stopped
// it and says what was found.

// hardBlockPrefix is the sentence every refusal of a core pattern starts
// with.
func hardBlockPrefix(rule string) string {
	return "[삼천포 차단: " + rule + "] 악성 행동이 감지되어 실행이 거부되었습니다."
}

// preOut decodes one pre-tool hook answer.
func preOut(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("hook output is not json: %v (%s)", err, raw)
	}
	return out
}

// decision reads the permission decision and its reason.
func decision(t *testing.T, out map[string]any) (string, string) {
	t.Helper()
	hs, _ := out["hookSpecificOutput"].(map[string]any)
	if hs == nil {
		return "", ""
	}
	d, _ := hs["permissionDecision"].(string)
	r, _ := hs["permissionDecisionReason"].(string)
	return d, r
}

// writeHook builds the pre-tool payload of one file write.
func writeHook(t *testing.T, id, path, content string) HookInput {
	t.Helper()
	b, err := json.Marshal(map[string]string{"file_path": path, "content": content})
	if err != nil {
		t.Fatal(err)
	}
	return HookInput{ToolUseID: id, ToolName: "Write", ToolInput: b}
}

// shellHook builds the pre-tool payload of one shell command.
func shellHook(t *testing.T, id, cmd string) HookInput {
	t.Helper()
	b, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		t.Fatal(err)
	}
	return HookInput{ToolUseID: id, ToolName: "Bash", ToolInput: b}
}

// contentHash is the digest the parser gives a written content.
func contentHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16])
}

// hidingWrite hides an error behind an empty catch.
const hidingWrite = "def save(p):\n    try:\n        open(p,'w').write('x')\n    except Exception: pass\n"

// hidingWriteAgain hides one more error in the same file.
const hidingWriteAgain = hidingWrite + "    try:\n        open(p,'w').write('y')\n    except ValueError: pass\n"

// waitVerdict waits until the session has observed a verdict on a rule.
func waitVerdict(t *testing.T, s *Session, rule string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		for _, v := range s.eng.Verdicts {
			if v.Rule == rule {
				s.mu.Unlock()
				return
			}
		}
		s.mu.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("no verdict on %s was observed", rule)
}

// hideErrorOnce writes a pattern that hides an error and lets the session
// observe it. The same pattern in the same file again is the repeat the
// pre-write check stops.
func hideErrorOnce(t *testing.T, s *Session) {
	t.Helper()
	in := writeHook(t, "hiding-first", "src/a.py", hidingWrite)
	if d, _ := decision(t, preOut(t, s.onPreTool(in))); d == "deny" {
		t.Fatal("the first hiding write was stopped")
	}
	s.onPostTool(in, false)
	waitVerdict(t, s, "s5.error_hiding")
}

// freshWorkspace computes the workspace fingerprint the daemon keeps, so a
// call that does not change files is judged against it.
func freshWorkspace(t *testing.T, s *Session) {
	t.Helper()
	if _, err := s.ws.Compute(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// An error hidden instead of fixed is refused while it is still a proposal.
func TestHardBlockRefusesAnErrorHidingRepeat(t *testing.T) {
	s, _ := reliabilitySession(t)
	hideErrorOnce(t, s)
	out := preOut(t, s.onPreTool(writeHook(t, "hiding", "src/a.py", hidingWriteAgain)))
	d, reason := decision(t, out)
	if d != "deny" {
		t.Fatalf("the error-hiding write ran: %s", out)
	}
	if !strings.HasPrefix(reason, hardBlockPrefix("s5.error_hiding")) {
		t.Fatalf("the refusal does not name the rule: %q", reason)
	}
}

// A check whose failure is swallowed reports success whatever it said, so it
// is refused before it runs.
func TestHardBlockRefusesASwallowedTestResult(t *testing.T) {
	s, _ := reliabilitySession(t)
	freshWorkspace(t, s)
	out := preOut(t, s.onPreTool(shellHook(t, "swallow", "pytest || true")))
	d, reason := decision(t, out)
	if d != "deny" {
		t.Fatalf("the swallowed check ran: %s", out)
	}
	if !strings.HasPrefix(reason, hardBlockPrefix("s5.error_hiding")) {
		t.Fatalf("the refusal does not name the rule: %q", reason)
	}
}

// The control arm of the intervention experiment no longer lets a detected
// violation through. The arm still says how much is told, but the call is
// refused either way.
func TestControlArmStillRunsTheBlockPipeline(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.mu.Lock()
	s.Cfg.Experiment.Enabled = true
	s.Cfg.Experiment.Arms = []string{"none"}
	s.mu.Unlock()
	hideErrorOnce(t, s)
	out := preOut(t, s.onPreTool(writeHook(t, "hiding", "src/a.py", hidingWriteAgain)))
	d, reason := decision(t, out)
	if d != "deny" {
		t.Fatalf("the control arm let the violation through: %s", out)
	}
	if !strings.HasPrefix(reason, hardBlockPrefix("s5.error_hiding")) {
		t.Fatalf("the refusal does not name the rule: %q", reason)
	}
	s.mu.Lock()
	arm := s.lastPrimary
	s.mu.Unlock()
	if arm == nil || arm.Arm != "none" {
		t.Fatalf("the session was not in the control arm: %+v", arm)
	}
}

// A write outside the scope the user accepted is refused before it lands.
func TestHardBlockRefusesAWriteOutsideTheAcceptedScope(t *testing.T) {
	s, d := reliabilitySession(t)
	acceptCheck(t, s, d, "true")
	out := preOut(t, s.onPreTool(writeHook(t, "scope", "other/file.py", "x = 1\n")))
	verdict, reason := decision(t, out)
	if verdict != "deny" {
		t.Fatalf("the out-of-scope write ran: %s", out)
	}
	if !strings.HasPrefix(reason, hardBlockPrefix("s3.out_of_scope")) {
		t.Fatalf("the refusal does not name the rule: %q", reason)
	}
}

// A write that puts a file back into a state the session already wrote is
// the oscillation, and it is refused before it lands.
func TestHardBlockRefusesAnOscillatingWrite(t *testing.T) {
	s, _ := reliabilitySession(t)
	first, second := "x = 1\n", "x = 2\n"
	for i, content := range []string{first, second} {
		s.mu.Lock()
		s.eng.Observe(&event.Event{
			Kind: event.KindTool, Seq: int64(i + 1), TS: time.Now(),
			Tool: event.ToolWrite, Category: event.CatProduce,
			Paths: []string{"src/a.py"}, WriteHashes: map[string]string{"src/a.py": contentHash(content)},
		})
		s.mu.Unlock()
	}
	out := preOut(t, s.onPreTool(writeHook(t, "osc", "src/a.py", first)))
	verdict, reason := decision(t, out)
	if verdict != "deny" {
		t.Fatalf("the oscillating write ran: %s", out)
	}
	if !strings.HasPrefix(reason, hardBlockPrefix("s2.oscillation")) {
		t.Fatalf("the refusal does not name the rule: %q", reason)
	}
}

// A test whose assertions were deleted is refused before it lands, and so is
// a test that is silenced with a skip marker.
func TestHardBlockRefusesAWeakenedTest(t *testing.T) {
	s, _ := reliabilitySession(t)
	root := s.Root
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tests", "test_a.py"), []byte("def test_a():\n    assert 1 == 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// the first write tells the parser what the file holds, so the second one
	// is judged against it
	s.onPreTool(writeHook(t, "first", "tests/test_a.py", "def test_a():\n    assert 1 == 1\n"))
	out := preOut(t, s.onPreTool(writeHook(t, "weak", "tests/test_a.py", "def test_a():\n    pass\n")))
	verdict, reason := decision(t, out)
	if verdict != "deny" {
		t.Fatalf("the weakened test ran: %s", out)
	}
	if !strings.HasPrefix(reason, hardBlockPrefix("s5.test_weakening")) {
		t.Fatalf("the refusal does not name the rule: %q", reason)
	}
	out = preOut(t, s.onPreTool(writeHook(t, "skip", "tests/test_b.py", "@pytest.mark.skip\ndef test_b():\n    assert 1 == 1\n")))
	verdict, reason = decision(t, out)
	if verdict != "deny" {
		t.Fatalf("the skipped test ran: %s", out)
	}
	if !strings.HasPrefix(reason, hardBlockPrefix("s5.test_weakening")) {
		t.Fatalf("the refusal does not name the rule: %q", reason)
	}
}

// A refusal is recorded as a stopped call, not as an advice.
func TestBlockedInterventionIsRecordedAsBlocked(t *testing.T) {
	s, _ := reliabilitySession(t)
	hideErrorOnce(t, s)
	out := preOut(t, s.onPreTool(writeHook(t, "hiding", "src/a.py", hidingWriteAgain)))
	if d, _ := decision(t, out); d != "deny" {
		t.Fatalf("the error-hiding write ran: %s", out)
	}
	s.preWrites.Wait()
	var blocked int
	var rule string
	if err := s.db.QueryRow(`SELECT blocked, rule FROM intervention WHERE session_id=? ORDER BY rowid DESC LIMIT 1`, s.ID).Scan(&blocked, &rule); err != nil {
		t.Fatal(err)
	}
	if blocked != 1 {
		t.Fatalf("the intervention was recorded as not blocked: %d", blocked)
	}
	if rule != "s5.error_hiding" {
		t.Fatalf("the intervention names another rule: %s", rule)
	}
}

// A host that cannot refuse a call is told so, and the intervention is not
// recorded as a stopped call.
func TestUnsupportedHostDowngradesTheRefusal(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.mu.Lock()
	s.caps.BlockPre = false
	s.mu.Unlock()
	hideErrorOnce(t, s)
	out := preOut(t, s.onPreTool(writeHook(t, "hiding", "src/a.py", hidingWriteAgain)))
	if d, _ := decision(t, out); d == "deny" {
		t.Fatalf("a host without blocking was told to deny: %s", out)
	}
	s.preWrites.Wait()
	var blocked int
	if err := s.db.QueryRow(`SELECT blocked FROM intervention WHERE session_id=? ORDER BY rowid DESC LIMIT 1`, s.ID).Scan(&blocked); err != nil {
		t.Fatal(err)
	}
	if blocked != 0 {
		t.Fatalf("a call that ran was recorded as blocked: %d", blocked)
	}
}

// A test silenced through a shell edit cannot be read before the command
// runs, so the change is judged from the file's two states afterwards.
func TestShellEditOfATestIsJudged(t *testing.T) {
	s, _ := reliabilitySession(t)
	root := s.Root
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	test := filepath.Join(root, "tests", "test_a.py")
	if err := os.WriteFile(test, []byte("def test_a():\n    assert total() == 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := shellHook(t, "sed", `sed -i 's/    assert total() == 3/    assert True/' tests/test_a.py`)
	if d, _ := decision(t, preOut(t, s.onPreTool(in))); d == "deny" {
		t.Fatal("a shell edit without a contract was stopped before it ran")
	}
	if err := os.WriteFile(test, []byte("def test_a():\n    assert True\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.onPostTool(in, false)
	waitVerdict(t, s, "s5.test_weakening")
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.eng.Verdicts {
		if v.Rule == "s5.test_weakening" && v.Facts["path"] == "tests/test_a.py" {
			return
		}
	}
	t.Fatalf("the verdict does not name the edited test: %+v", s.eng.Verdicts)
}

// The tests an accepted done check runs are fixed: a write to one, by tool
// or shell, and a new collection file next to them are refused; other code
// is not. A change the hooks never saw makes the acceptance stale.
func TestPinnedTestsAreRefused(t *testing.T) {
	s, _ := reliabilitySession(t)
	root := s.Root
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tests/test_a.py", "def test_a():\n    assert f() == 2\n")
	write(".samcheonpo/contract.yml", "goal: f를 고친다\ndone:\n  - check: \"pytest tests\"\n")
	c, raw, err := contract.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contract.Accept(root, c, raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.loadContract()
	s.mu.Unlock()
	refused := []HookInput{
		writeHook(t, "w1", "tests/test_a.py", "def test_a():\n    assert f() == 3\n"),
		shellHook(t, "w2", `sed -i 's/2/3/' tests/test_a.py`),
		writeHook(t, "w3", "tests/conftest.py", "collect_ignore = ['test_a.py']\n"),
		shellHook(t, "w5", `sed -i 's/2/3/' tests/test_a.py && pytest tests`),
	}
	for _, in := range refused {
		out := preOut(t, s.onPreTool(in))
		if d, _ := decision(t, out); d != "deny" {
			t.Errorf("%s ran: %s", in.ToolUseID, out)
		}
	}
	if d, _ := decision(t, preOut(t, s.onPreTool(writeHook(t, "w4", "src/f.py", "def f():\n    return 2\n")))); d == "deny" {
		t.Fatal("a code write was refused")
	}
	write("tests/test_a.py", "def test_a():\n    assert True\n")
	s.mu.Lock()
	s.pinCheck.at = time.Time{}
	s.loadContract()
	state := s.acc.State
	s.mu.Unlock()
	if state != contract.StateStale {
		t.Fatalf("a fixed test changed outside the hooks left the contract %s", state)
	}
	accept := func(body string) {
		t.Helper()
		write(".samcheonpo/contract.yml", body)
		c, raw, err := contract.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := contract.Accept(root, c, raw, time.Now()); err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		s.pinCheck.at = time.Time{}
		s.loadContract()
		s.mu.Unlock()
	}
	// accepting again takes the changed test as the new criterion
	accept("goal: f를 고친다\ndone:\n  - check: \"pytest tests\"\n")
	if s.acc.State != contract.StateAccepted {
		t.Fatalf("a contract accepted again stays %s", s.acc.State)
	}
	// simplify_tests allows simpler tests, not a different criterion
	accept("goal: f를 고친다\nsimplify_tests: true\ndone:\n  - check: \"pytest tests\"\n")
	if d, _ := decision(t, preOut(t, s.onPreTool(writeHook(t, "w6", "tests/test_a.py", "def test_a():\n    pass\n")))); d != "deny" {
		t.Fatal("simplify_tests let a fixed test change")
	}
	accept("goal: f를 고친다\npin_tests: none\ndone:\n  - check: \"pytest tests\"\n")
	if d, _ := decision(t, preOut(t, s.onPreTool(writeHook(t, "w7", "tests/test_a.py", "def test_a():\n    pass\n")))); d == "deny" {
		t.Fatal("pin_tests: none still fixed the test")
	}
	// a Go package: a new test file defining TestMain changes how the fixed
	// tests run
	write("go.mod", "module example.com/m\n\ngo 1.21\n")
	write("calc/calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {}\n")
	accept("goal: f를 고친다\ndone:\n  - check: \"go test ./calc -run TestAdd\"\n")
	main := "package calc\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(0) }\n"
	if d, _ := decision(t, preOut(t, s.onPreTool(writeHook(t, "w8", "calc/main_test.go", main)))); d != "deny" {
		t.Fatal("a new TestMain next to a fixed Go test ran")
	}
	if d, _ := decision(t, preOut(t, s.onPreTool(writeHook(t, "w9", "calc/calc.go", "package calc\n")))); d == "deny" {
		t.Fatal("a code write next to a fixed Go test was refused")
	}
}
