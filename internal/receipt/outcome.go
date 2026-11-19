package receipt

import (
	"fmt"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
)

// Outcome is what the user went through in a session, put above the money:
// how often they scolded the agent, how often a correction they had
// already given came back, and what the scolded work cost.
type Outcome struct {
	// Observed is false when the session gave no user input to read (a
	// scheduled run): no count is known then, not zero.
	Observed   bool `json:"observed"`
	Reproaches int  `json:"reproaches"`
	// Repeats counts corrections given again; RepeatsKnown is false when
	// the corrections could not be read.
	Repeats      int  `json:"repeats"`
	RepeatsKnown bool `json:"repeats_known"`
	// Priced is false when a scolded span had calls without a price; the
	// cost is then shown in tokens.
	Priced       bool  `json:"priced"`
	SpanMicro    int64 `json:"span_micro_krw"`
	SpanTokens   int64 `json:"span_tokens"`
	ReworkMicro  int64 `json:"rework_micro_krw"`
	ReworkTokens int64 `json:"rework_tokens"`
	// Candidates are correction rules waiting for the user's approval.
	Candidates []Candidate `json:"candidates,omitempty"`
}

// Candidate is a correction rule the user can approve.
type Candidate struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
	Times     int    `json:"times"`
}

func outcomeAmount(priced bool, micro, tokens int64) string {
	if priced {
		return contract.Comma(cost.Won(micro)) + lang.T("원")
	}
	return HumanTokens(tokens)
}

// Line is the first line of the receipt.
func (o *Outcome) Line() string {
	if o == nil || !o.Observed {
		return lang.T("질책 미관측: 사용자 입력을 받지 못한 세션")
	}
	if o.Reproaches == 0 {
		return lang.T("질책 없음")
	}
	repeats := lang.T("미집계")
	if o.RepeatsKnown {
		repeats = fmt.Sprintf(lang.T("%d회"), o.Repeats)
	}
	line := fmt.Sprintf(lang.T("질책 %d회 · 같은 지적 재발 %s · 질책 직전 비용 %s"), o.Reproaches, repeats, outcomeAmount(o.Priced, o.SpanMicro, o.SpanTokens))
	if o.ReworkMicro > 0 || o.ReworkTokens > 0 {
		line += fmt.Sprintf(lang.T(" (재작업으로 확인된 비용 %s)"), outcomeAmount(o.Priced, o.ReworkMicro, o.ReworkTokens))
	}
	return line
}

// candidateLines are the rule candidates and how to approve them.
func (o *Outcome) candidateLines() []string {
	if o == nil || len(o.Candidates) == 0 {
		return nil
	}
	var out []string
	for _, c := range o.Candidates {
		out = append(out, fmt.Sprintf(lang.T("%s (지적 %d회) — 승인: `samcheonpo rules corrections approve %s`"), c.Statement, c.Times, c.ID))
	}
	return out
}

func (o *Outcome) markdown(b *strings.Builder) {
	b.WriteString("**" + mdEsc(o.Line()) + "**\n\n")
}

func (o *Outcome) candidatesMarkdown(b *strings.Builder) {
	lines := o.candidateLines()
	if len(lines) == 0 {
		return
	}
	b.WriteString(lang.T("\n### 규칙 후보 · 승인하면 실행\n\n"))
	for _, l := range lines {
		b.WriteString("- " + l + "\n")
	}
	b.WriteString(lang.T("\n승인한 규칙을 에이전트도 알게 하려면 CLAUDE.md에 같은 문장을 더한다:\n\n```diff\n"))
	for _, c := range o.Candidates {
		b.WriteString("+ - " + c.Statement + "\n")
	}
	b.WriteString("```\n")
}
