package receipt

import (
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
)

type Statement struct {
	Text     string  `json:"text"`
	Evidence []int64 `json:"evidence,omitempty"`
}

// ActionSummary deliberately separates observed facts, inferred labels and
// unknown outcomes. Rendering a recommendation does not execute it.
type ActionSummary struct {
	Facts      []Statement `json:"facts"`
	Estimates  []Statement `json:"estimates"`
	Unknowns   []string    `json:"unknowns"`
	NextAction string      `json:"next_action"`
}

func SummarizeAction(in Input) ActionSummary {
	s := ActionSummary{NextAction: lang.T("아직 확인되지 않은 완료 조건을 먼저 확인한다. 새 변경이나 실패 근거가 없으면 검사를 반복하지 않는다.")}
	if in.Criteria[1] > 0 {
		s.Facts = append(s.Facts, Statement{Text: fmt.Sprintf(lang.T("기록된 완료 조건 %d개 중 %d개에 충족 근거가 있다"), in.Criteria[1], in.Criteria[0])})
		if in.Criteria[0] < in.Criteria[1] {
			s.Unknowns = append(s.Unknowns, lang.T("남은 조건과 수동 확인은 완료로 인정하지 않았다"))
		}
	} else {
		s.Unknowns = append(s.Unknowns, lang.T("수락된 완료 조건의 충족 여부가 확인되지 않았다"))
	}
	var strongest *detect.Signal
	for i := range in.Verdicts {
		v := &in.Verdicts[i]
		if v.Suppressed || !v.Primary {
			continue
		}
		if strongest == nil || v.Level > strongest.Level || v.Level == strongest.Level && v.Seq > strongest.Seq {
			strongest = v
		}
	}
	if strongest != nil {
		st := Statement{Text: Describe(*strongest), Evidence: append([]int64(nil), strongest.Evidence...)}
		if strongest.Estimate || strongest.Confidence < 1 {
			s.Estimates = append(s.Estimates, st)
		} else {
			s.Facts = append(s.Facts, st)
		}
		p := recovery.For(recovery.Diagnose(strongest.Rule, "", false))
		s.NextAction = p.Action + " " + p.StopCondition
	}
	if len(in.Recoveries) > 0 && in.Recoveries[len(in.Recoveries)-1].Observation != "intent_revision_changed" {
		a := in.Recoveries[len(in.Recoveries)-1]
		s.NextAction = a.Prescription.Action + " " + a.Prescription.StopCondition
		if a.DeliveredAt == nil {
			s.Unknowns = append(s.Unknowns, lang.T("복구 처방의 전달이 확인되지 않았다"))
		} else {
			s.Facts = append(s.Facts, Statement{Text: lang.T("복구 메시지가 훅 클라이언트 출력까지 전달됐다(수행 여부는 별도)")})
		}
		switch a.Observation {
		case "recurrence_observed":
			s.Facts = append(s.Facts, Statement{Text: lang.T("관찰창에서 같은 경고가 다시 나타났다")})
		case "alternative_observed":
			s.Facts = append(s.Facts, Statement{Text: lang.T("개입 뒤 관측: 처방이 바라는 다음 행동이 나타났다")})
			s.Unknowns = append(s.Unknowns, lang.T("개입이 그 행동의 원인인지는 입증되지 않는다"))
		case "no_recurrence_observed":
			s.Facts = append(s.Facts, Statement{Text: lang.T("관찰창에서 같은 경고가 재발하지 않았다")})
			s.Unknowns = append(s.Unknowns, lang.T("재발 없음만으로 문제 해결이나 개입 효과는 입증되지 않는다"))
		default:
			s.Unknowns = append(s.Unknowns, lang.T("복구 효과를 판단할 관찰 근거가 부족하다"))
		}
	}
	return s
}

func (s ActionSummary) Text() string {
	var b strings.Builder
	for _, f := range s.Facts {
		fmt.Fprintf(&b, lang.T("관측: %s\n"), f.Text)
	}
	for _, f := range s.Estimates {
		fmt.Fprintf(&b, lang.T("추정: %s\n"), f.Text)
	}
	for _, u := range s.Unknowns {
		fmt.Fprintf(&b, lang.T("미확인: %s\n"), u)
	}
	if s.NextAction != "" {
		fmt.Fprintf(&b, lang.T("다음 행동: %s\n"), s.NextAction)
	}
	return b.String()
}
