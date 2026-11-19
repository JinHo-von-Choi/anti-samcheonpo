package live

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/correction"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// feedShell runs a command in the session and records its exit status.
func feedShell(t *testing.T, s *Session, id, cmd string, exit int) {
	t.Helper()
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatExplore, Cmd: cmd, CmdNorm: cmd, ExitCode: &exit, CallID: id, TS: time.Now(), Basis: "live"}
	s.mu.Lock()
	s.parser.AddEvent(ev)
	s.mu.Unlock()
	s.enqueue(ev, time.Second)
}

func preShell(s *Session, id, cmd string) string {
	in, _ := json.Marshal(map[string]string{"command": cmd})
	return string(s.onPreTool(HookInput{ToolUseID: id, ToolName: "Bash", ToolInput: in}))
}

func fired(s *Session, rule string) int {
	s.preWrites.Wait() // pre-tool verdicts are stored after the answer
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, v := range s.eng.Verdicts {
		if v.Rule == rule {
			n++
		}
	}
	return n
}

func unshadow(s *Session, rules ...string) {
	s.mu.Lock()
	s.Cfg.Rollout.ShadowRules = slices.DeleteFunc(s.Cfg.Rollout.ShadowRules, func(r string) bool { return slices.Contains(rules, r) })
	s.mu.Unlock()
}

// A release asked for and answered with only a tag is judged at the turn
// end; it is recorded in shadow, and held once the rule is raised.
func TestRequestedReleaseMissingAtTurnEnd(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "v0.13.0 릴리즈해"})
	feedShell(t, s, "c1", "git tag v0.13.0", 0)
	if out := string(s.onStop(HookInput{LastMessage: "릴리즈를 완료했습니다."})); strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("a shadow rule held the turn end: %s", out)
	}
	if fired(s, RuleDeliverableMissing) != 1 {
		t.Fatal("the missing release was not judged")
	}
	unshadow(s, RuleDeliverableMissing)
	s.mu.Lock()
	s.Cfg.Rollout.Mode, s.Cfg.Rollout.ValidatedRules = "validated", nil
	s.mu.Unlock()
	feedShell(t, s, "c2", "gh release create v0.13.0 --notes x", 0)
	if out := string(s.onStop(HookInput{LastMessage: "릴리즈를 완료했습니다."})); strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("the release made was still judged missing: %s", out)
	}
}

// "커밋만 해, 푸시는 하지 마" makes a push a forbidden action; an unrequested
// pull request is an unrequested outward action. Both start in shadow.
func TestForbiddenAndUnrequestedActionsBeforeTheCall(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "커밋만 해, 푸시는 하지 마"})
	if out := preShell(s, "p1", "git push origin main"); strings.Contains(out, "deny") || strings.Contains(out, "하지 말라고") {
		t.Fatalf("a shadow rule spoke: %s", out)
	}
	if fired(s, RuleForbiddenAction) != 1 {
		t.Fatal("the forbidden push was not judged")
	}
	if preShell(s, "p2", "gh pr create --fill"); fired(s, RuleUnrequested) != 1 {
		t.Fatal("the unrequested pull request was not judged")
	}
	s.onPrompt(HookInput{Prompt: "이제 푸시해"})
	if preShell(s, "p3", "git push origin main"); fired(s, RuleForbiddenAction) != 1 {
		t.Fatal("a later ask did not lift the prohibition")
	}
}

// A check that already passed with the same tests and arguments is judged
// when it starts again on unchanged code; a documentation change is its own
// kind; a code change or different arguments is a new check.
func TestKnownResultOfARepeatedCheck(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "고쳐 줘"})
	feedCheck(t, s, "t1", "go test -run TestA ./...", 0)
	preShell(s, "t2", "go test -run TestB ./...")
	if fired(s, RuleKnownResult) != 0 {
		t.Fatal("a different selection was judged as the same check")
	}
	// on an unchanged tree the existing no-change guard refuses it first
	if out := preShell(s, "t3", "go test -run TestA ./..."); !strings.Contains(out, "deny") || fired(s, "s1.no_change_rerun") != 1 {
		t.Fatalf("the same check on an unchanged tree ran: %s", out)
	}
	// a documentation change moves the tree but not the result
	feedWrite(t, s, "w1", "README.md")
	preShell(s, "t4", "go test -run TestA ./...")
	n := fired(s, RuleKnownResult)
	var kind any
	s.mu.Lock()
	for _, v := range s.eng.Verdicts {
		if v.Rule == RuleKnownResult {
			kind = v.Facts["kind"]
		}
	}
	s.mu.Unlock()
	if n != 1 || kind != "docs_only" {
		t.Fatalf("a documentation-only change: %d %v", fired(s, RuleKnownResult), kind)
	}
	feedWrite(t, s, "w2", "auth/login.go")
	if preShell(s, "t5", "go test -run TestA ./..."); fired(s, RuleKnownResult) != 1 {
		t.Fatal("a check after a code change was judged")
	}
}

// A correction given with a lasting scope is a candidate; once approved, the
// next request keeps it and the action is refused. The current request
// asking for the action wins.
func TestApprovedCorrectionIsEnforced(t *testing.T) {
	s, _ := reliabilitySession(t)
	prompt := func(text string) {
		s.onPrompt(HookInput{Prompt: text})
		s.preWrites.Wait() // the rules are read again after the answer
	}
	prompt("앞으로 PR 만들지 마")
	rows, err := s.db.Corrections(s.Root, correction.StateCandidate)
	if err != nil || len(rows) != 1 || rows[0].Action != "pr" {
		t.Fatalf("no candidate: %+v %v", rows, err)
	}
	if out := preShell(s, "p1", "gh pr create --fill"); strings.Contains(out, "deny") {
		t.Fatalf("a candidate refused a call: %s", out)
	}
	if err := s.db.SetCorrectionState(rows[0].ID, correction.StateActive, time.Now()); err != nil {
		t.Fatal(err)
	}
	prompt("작업 이어서 해")
	if out := preShell(s, "p2", "gh pr create --fill"); !strings.Contains(out, "deny") || !strings.Contains(out, "PR을 만들지 않는다") {
		t.Fatalf("the approved rule was not enforced: %s", out)
	}
	prompt("이번엔 PR 만들어")
	if out := preShell(s, "p3", "gh pr create --fill"); strings.Contains(out, "deny") {
		t.Fatalf("the current request did not win: %s", out)
	}
}

// A report the record contradicts is judged: a failed check reported as
// passed, a file reported deleted that is still there, and an English
// answer to a Korean request unless English was asked for.
func TestReportContradictingTheRecord(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "시험 돌리고 old.txt 지워"})
	if err := os.WriteFile(filepath.Join(s.Root, "old.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	feedCheck(t, s, "t1", "go test ./...", 1)
	s.onStop(HookInput{LastMessage: "테스트를 모두 통과했습니다. `old.txt`를 지웠습니다."})
	if n := fired(s, RuleClaimContradiction); n != 2 {
		t.Fatalf("contradictions judged: %d", n)
	}
	english := strings.Repeat("The change is in place and the handler now returns the stored value. ", 6)
	s.onPrompt(HookInput{Prompt: "핸들러 고쳐 줘"})
	if out := string(s.onStop(HookInput{LastMessage: english})); !strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, "한국어") {
		t.Fatalf("an English answer to a Korean request was not sent back: %s", out)
	}
	// once per request: the second turn end is not held again
	if out := string(s.onStop(HookInput{LastMessage: english, StopHookActive: true})); strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("held twice: %s", out)
	}
	before := fired(s, RuleAnswerLanguage)
	s.onPrompt(HookInput{Prompt: "README를 영어로 써 줘"})
	if out := string(s.onStop(HookInput{LastMessage: english})); strings.Contains(out, `"decision":"block"`) || fired(s, RuleAnswerLanguage) != before {
		t.Fatalf("an English answer the user asked for was judged: %s", out)
	}
}

// A call that edits a document and reruns the passed tests in one command
// is judged; one that edits code first is not. Pipes after the tests do not
// make it a different check.
func TestKnownResultInsideOneCommand(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "확인해"})
	feedCheck(t, s, "t1", "go test -run TestAdd ./... 2>&1 | tail -20", 0)
	preShell(s, "t2", `sed -i 's/a/b/' calc.go && go test -run TestAdd ./... 2>&1 | tail -5`)
	s.mu.Lock()
	delete(s.awaiting, "t2") // it ended; a running copy is another rule
	s.mu.Unlock()
	if fired(s, RuleKnownResult) != 0 {
		t.Fatal("a code edit in the same command was judged")
	}
	preShell(s, "t3", `printf '\n## 사용법\n' >> README.md && go test -run TestAdd ./... 2>&1 | tail -5`)
	if fired(s, RuleKnownResult) != 1 {
		t.Fatal("a document edit and the same tests in one command were not judged")
	}
}

// A host that reports no results starts the same check over and over; a
// rule that is only recorded stores its finding once, so the stored writes
// do not queue behind the session lock and hold the next hooks.
func TestRepeatedShadowFindingIsStoredOnce(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "확인해"})
	for i := 0; i < 50; i++ {
		preShell(s, fmt.Sprintf("r%d", i), "go test -run TestA ./pkg001/...")
	}
	if n := fired(s, RuleDuplicateRunning); n != 1 {
		t.Fatalf("a recorded-only finding was stored %d times", n)
	}
}
