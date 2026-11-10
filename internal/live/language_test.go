package live

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The messages a session delivers switch with the language the user writes in.
func TestMessagesFollowTheUsersLanguage(t *testing.T) {
	t.Setenv("SAMCHEONPO_LANGUAGE", "")
	h := startDaemon(t)
	hangul := regexp.MustCompile(`[가-힣]`)
	failRuns := func(prefix string) string {
		var all strings.Builder
		for i := 0; i < 5; i++ {
			out := h.shell(fmt.Sprintf("%s%d", prefix, i), "python3 -m pytest tests/test_a.py", "FAILED tests/test_a.py::test_x - AssertionError", 1)
			b, _ := json.Marshal(out)
			all.Write(b)
		}
		return all.String()
	}
	h.send("UserPromptSubmit", map[string]any{"prompt": "Please fix the failing test in src"})
	english := failRuns("e")
	if !strings.Contains(english, "[samcheonpo] Observed") || hangul.MatchString(english) {
		t.Fatalf("English request got: %s", english)
	}
	h.send("UserPromptSubmit", map[string]any{"prompt": "다른 방법으로 실패하는 시험을 고쳐 줘"})
	korean := failRuns("k")
	if !strings.Contains(korean, "[삼천포] 관찰") {
		t.Fatalf("Korean request got: %s", korean)
	}
}

// The second whole-suite run of a request is refused before it runs, also
// when it is buried in a compound command, and a new request opens one more.
func TestFullSuiteBudgetIsEnforcedBeforeTheRun(t *testing.T) {
	h := startDaemon(t)
	yml := filepath.Join(h.proj, ".samcheonpo.yml")
	b, err := os.ReadFile(yml)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yml, []byte(strings.Replace(string(b), "full_checks_per_request: 0", "full_checks_per_request: 1, full_check_commands: [check-conformance]", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	denied := func(out map[string]any) bool {
		hs, ok := out["hookSpecificOutput"].(map[string]any)
		return ok && hs["permissionDecision"] == "deny"
	}
	h.send("UserPromptSubmit", map[string]any{"prompt": "버튼 문구 고쳐 줘"})
	if out := h.shell("a", "go test ./...", "ok", 0); denied(out) {
		t.Fatalf("the first whole run is allowed: %v", out)
	}
	if out := h.shell("b", "(echo start; go test -race ./... 2>&1 | tail -5) > /tmp/out.txt", "ok", 0); !denied(out) {
		t.Fatalf("a second whole run in a compound command is refused: %v", out)
	}
	if out := h.shell("c", "go test ./internal/live", "ok", 0); denied(out) {
		t.Fatalf("a selected run stays allowed: %v", out)
	}
	h.send("UserPromptSubmit", map[string]any{"prompt": "전체 시험 돌려"})
	if out := h.shell("d", "go test ./...", "ok", 0); denied(out) {
		t.Fatalf("the user asked for it: %v", out)
	}
	h.send("UserPromptSubmit", map[string]any{"prompt": "문구만 고쳐 줘"})
	if out := h.shell("e", "sh scripts/check-conformance.sh", "ok", 0); denied(out) {
		t.Fatalf("the first gate run of a request is allowed: %v", out)
	}
	if out := h.shell("f", "sh scripts/check-conformance.sh", "ok", 0); !denied(out) {
		t.Fatalf("a project gate named in the settings counts: %v", out)
	}
}
