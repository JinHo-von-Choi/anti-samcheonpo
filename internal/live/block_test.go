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
