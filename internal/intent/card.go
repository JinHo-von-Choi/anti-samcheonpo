package intent

import (
	"fmt"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
)

// Card is a presentation of intent and existing authority, never a grant.
type Card struct {
	TaskID        string               `json:"task_id,omitempty"`
	Revision      uint64               `json:"revision,omitempty"`
	Source        *Source              `json:"source,omitempty"`
	LatestMessage string               `json:"latest_message,omitempty"`
	Goal          string               `json:"goal"`
	RequestedGoal string               `json:"requested_goal,omitempty"`
	Certainty     string               `json:"certainty"`
	Authority     string               `json:"authority"`
	State         contract.State       `json:"state"`
	Criteria      []contract.Check     `json:"criteria"`
	Allowed       []string             `json:"allowed"`
	Protected     []string             `json:"protected"`
	Forbidden     []string             `json:"forbidden"`
	BudgetKRW     int64                `json:"budget_krw"`
	BudgetMinutes int                  `json:"budget_minutes"`
	Candidates    []contract.Candidate `json:"candidates,omitempty"`
}

func GoalCard(c *contract.Contract, acceptance contract.Acceptance, requested string, candidates []contract.Candidate) Card {
	card := Card{RequestedGoal: requested, State: acceptance.State, Certainty: "draft", Authority: "자동 검사 실행 미승인", Candidates: candidates}
	if card.State == "" {
		card.State = contract.StateDraft
	}
	if c != nil {
		card.Goal = c.Goal
		card.Criteria = append([]contract.Check(nil), c.Done...)
		card.Allowed = append([]string(nil), c.Scope.Allow...)
		card.Protected = append([]string(nil), c.Scope.Protect...)
		card.Forbidden = append([]string(nil), c.Forbid...)
		card.BudgetKRW, card.BudgetMinutes = c.Budget.KRW, c.Budget.Minutes
		if acceptance.State == contract.StateAccepted && acceptance.ChecksHash == c.ChecksHash() && acceptance.AuthorityHash == contract.AuthorityDigest(c) && acceptance.RequestedGoal == "" {
			card.Certainty = "accepted_contract"
			card.Authority = "수락된 계약의 검사만 실행 가능; 새 명령·권한 확대 승인 아님"
		}
	}
	if acceptance.RequestedGoal != "" {
		card.RequestedGoal = acceptance.RequestedGoal
	}
	return card
}

func (c Card) Text() string {
	var b strings.Builder
	b.WriteString("AI는 이렇게 이해했습니다.\n")
	if c.TaskID != "" {
		fmt.Fprintf(&b, "작업 %s · 의도 개정 %d\n", c.TaskID, c.Revision)
	}
	if c.Source != nil {
		fmt.Fprintf(&b, "요청 출처: %s / %s / %s\n", c.Source.Origin, c.Source.SessionID, c.Source.EventID)
	}
	if c.LatestMessage != "" && c.LatestMessage != c.RequestedGoal {
		fmt.Fprintf(&b, "최근 요청 기록: %s\n", c.LatestMessage)
	}
	fmt.Fprintf(&b, "[목표 카드] 상태: %s · 확실성: %s\n권한: %s\n", c.State, c.Certainty, c.Authority)
	if c.Goal != "" {
		fmt.Fprintf(&b, "계약에 적힌 목표: %s\n", c.Goal)
	}
	if c.RequestedGoal != "" {
		fmt.Fprintf(&b, "사용자 요청(초안에 반영할 내용): %s\n", c.RequestedGoal)
	}
	for _, check := range c.Criteria {
		if check.Manual != "" {
			fmt.Fprintf(&b, "직접 확인할 조건: %s\n", check.Manual)
		} else {
			fmt.Fprintf(&b, "기계 확인 조건: %s\n", check.Check)
			if check.Reuse != nil {
				fmt.Fprintf(&b, "  재사용 선언: 입력·환경 동일 시 %d초; 외부 의존성·flaky 검사 제외\n", check.Reuse.MaxAgeSec)
			}
		}
	}
	if len(c.Allowed) > 0 {
		fmt.Fprintf(&b, "허용 범위: %s\n", strings.Join(c.Allowed, ", "))
	}
	if len(c.Protected) > 0 {
		fmt.Fprintf(&b, "보호 범위: %s\n", strings.Join(c.Protected, ", "))
	}
	if len(c.Forbidden) > 0 {
		fmt.Fprintf(&b, "금지 사항: %s\n", strings.Join(c.Forbidden, ", "))
	}
	if c.BudgetKRW > 0 {
		fmt.Fprintf(&b, "설정된 API 환산 예산: %s원(실제 청구액 아님)\n", contract.Comma(c.BudgetKRW))
	}
	if c.BudgetMinutes > 0 {
		fmt.Fprintf(&b, "설정된 시간 예산: %d분\n", c.BudgetMinutes)
	}
	if c.Certainty != "accepted_contract" {
		b.WriteString("초안·후보 발견은 실행 승인이 아니다. 내용을 확인하고 accept하거나 edit로 고친다.\n")
		b.WriteString("[/samcheonpo:accept 맞아요]  [/samcheonpo:edit 고칠래요]  [/samcheonpo:skip 계약 없이 진행]\n")
	}
	return b.String()
}
