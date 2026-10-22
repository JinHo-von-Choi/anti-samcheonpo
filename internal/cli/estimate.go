package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/receipt"
)

// loopRules are the rules whose interventions are repetitions: the agent
// was about to do the same thing again.
var loopRules = map[string]bool{
	"s2.semantic_oscillation": true,
	"s2.behavior_loop":        true,
	"s1.delegation_loop":      true,
	"s1.linked_loop":          true,
}

// estimateStoppedRepetitions estimates what the stopped repetitions would
// have cost.
//
// The estimate is labelled, and it is not a saving. What the monitor
// stopped is counted, and the size of one repetition is assumed from the
// session's own turns. The assumption is stated next to the number.
//
// An observation that was cut short cannot carry an estimate: the turns
// after the cut are unknown, so the sample would describe a part of the
// session as if it were the whole.
func estimateStoppedRepetitions(db *ledger.DB, s *event.Session, prices *cost.Table, now time.Time) *receipt.Estimate {
	if s == nil || db == nil {
		return nil
	}
	if obs, err := db.Observation(s.ID); err == nil {
		switch obs.CloseReason {
		case ledger.ObservationDaemonLost, ledger.ObservationIdleTimeout:
			return nil
		}
	}
	counts, err := db.BlockedRulesForSession(s.ID)
	if err != nil {
		return nil
	}
	blocked := 0
	var rules []string
	for rule, n := range counts {
		if loopRules[rule] {
			blocked += n
			rules = append(rules, rule)
		}
	}
	if blocked == 0 {
		return nil
	}
	sort.Strings(rules)
	evs, err := db.EventsOf(s.ID)
	if err != nil {
		return nil
	}
	var in, out, turns int64
	for _, ev := range evs {
		if ev.Usage.In+ev.Usage.Out == 0 {
			continue
		}
		in += ev.Usage.In
		out += ev.Usage.Out
		turns++
	}
	if turns == 0 {
		return nil
	}
	avgIn, avgOut := in/turns, out/turns
	est := &receipt.Estimate{
		Label: "차단된 반복의 추정 비용",
		Basis: fmt.Sprintf("이 세션에서 실행 전에 멈춘 반복 %d회 (%s)", blocked, strings.Join(rules, ", ")),
		Assumption: fmt.Sprintf("각 반복이 이 세션의 평균 회차(%d회, 입력 %d·출력 %d토큰)만큼 더 소모했다고 가정한 값이다. 관측값이 아니라 가정이다",
			turns, avgIn, avgOut),
		Count:  blocked,
		Tokens: int64(blocked) * (avgIn + avgOut),
	}
	if prices != nil && s.Model != "" {
		if _, ok := prices.Lookup(s.Model, now); ok {
			if micro, ok := prices.MicroKRW(event.Usage{Model: s.Model, In: avgIn * int64(blocked), Out: avgOut * int64(blocked)}, now); ok {
				est.Micro = micro
				est.Assumption += fmt.Sprintf(" 단가는 %s 기준(확인일 %s)", s.Model, now.Format("2006-01-02"))
			}
		}
	}
	return est
}
