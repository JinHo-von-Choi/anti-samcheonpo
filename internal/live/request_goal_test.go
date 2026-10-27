package live

import (
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// A later request adds to the task. Warnings name the latest request and
// keep the earlier one, instead of repeating the first request forever.
func TestLaterRequestBecomesTheGoalOfWarnings(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "계산기 버그 고쳐 줘"})
	s.onPrompt(HookInput{Prompt: "원장 기록을 분석해서 효과를 검증해 줘"})
	s.mu.Lock()
	defer s.mu.Unlock()
	goal := s.msgContext().Goal
	if !strings.Contains(goal, "최신 요청(원장 기록을 분석해서") || !strings.Contains(goal, "이전 요청 1개") {
		t.Fatalf("goal %q", goal)
	}
	if strings.Contains(goal, "첫 요청") {
		t.Fatalf("goal still pinned to the first request: %q", goal)
	}
	text := s.eng.RequestText()
	if !strings.Contains(text, "계산기 버그") || !strings.Contains(text, "원장 기록") {
		t.Fatalf("requests lost: %q", text)
	}
}

// A redirect starts a new task, so the earlier followups are dropped.
func TestRedirectDropsEarlierRequests(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "계산기 버그 고쳐 줘"})
	s.onPrompt(HookInput{Prompt: "덧셈도 같이 봐 줘"})
	s.onPrompt(HookInput{Prompt: "목표 변경: 로그 화면 만들기"})
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.eng.Requests) != 0 || s.eng.LatestRequest() != "로그 화면 만들기" {
		t.Fatalf("requests %q latest %q", s.eng.Requests, s.eng.LatestRequest())
	}
}

// The summary and the HUD follow the latest request and list the named
// targets no change mentions yet.
func TestSummaryListsRemainingNamedTargets(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "calc.go에 `Add`, `Sub` 함수 구현해 줘"})
	s.onPrompt(HookInput{Prompt: "`Mul`도 추가해 줘"})
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Paths: []string{"calc.go"}, CallID: "e2", TS: time.Now(), Basis: "live",
		Patch: []event.PatchFile{{Path: "calc.go", Added: []string{"func Add(a, b int) int { return a + b }"}}}}
	s.mu.Lock()
	s.parser.AddEvent(ev)
	s.mu.Unlock()
	s.enqueue(ev, time.Second)
	sum := s.plainSummary()
	if !strings.Contains(sum, "`Mul`도 추가해 줘") || !strings.Contains(sum, "이전 요청 1개") {
		t.Fatalf("summary does not follow the latest request:\n%s", sum)
	}
	if !strings.Contains(sum, "아직 어떤 변경에도 없는 것: sub, mul") {
		t.Fatalf("remaining targets missing:\n%s", sum)
	}
	s.mu.Lock()
	h := s.hudState()
	s.mu.Unlock()
	if !strings.Contains(h.Requested, "최신 요청") {
		t.Fatalf("HUD request %q", h.Requested)
	}
}

// A background task notification delivered in the user turn is not a
// request: the goal and the intent revision stay as the user left them.
func TestHarnessNotificationIsNotARequest(t *testing.T) {
	s, _ := reliabilitySession(t)
	s.onPrompt(HookInput{Prompt: "계산기 버그 고쳐 줘"})
	s.mu.Lock()
	rev := s.intentRevision.Number
	s.mu.Unlock()
	s.onPrompt(HookInput{Prompt: "<task-notification>\n<task-id>b1</task-id>\n<status>completed</status>\n</task-notification>"})
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.eng.Requests) != 0 || s.eng.LatestRequest() != "계산기 버그 고쳐 줘" {
		t.Fatalf("notification became a request: %q", s.eng.Requests)
	}
	if s.intentRevision.Number != rev {
		t.Fatal("notification revised the intent")
	}
}
