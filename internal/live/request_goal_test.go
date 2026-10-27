package live

import (
	"strings"
	"testing"
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
