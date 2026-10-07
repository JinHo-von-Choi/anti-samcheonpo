package live

import (
	"errors"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func (s *Session) saveJudgeBudget() error {
	if s.db == nil {
		return errors.New("판정 예산 원장이 없어 외부 호출을 허용하지 않는다")
	}
	return s.db.SaveJudgeBudget(s.Agent, s.ID, s.judgeBudget)
}

func (s *Session) restoreJudgeBudget() error {
	if s.db == nil {
		return nil
	}
	b, err := s.db.JudgeBudget(s.Agent, s.ID)
	if err != nil {
		return err
	}
	s.judgeBudget = b
	if b.Pending {
		s.judgeBudget.Settle(0, false)
		if err = s.saveJudgeBudget(); err != nil {
			return err
		}
	}
	if s.judgeBudget.Unknown {
		s.judgeBudgetReason = "이전 판정 비용 미확인: 재시작 후에도 추가 호출 중단"
	}
	if s.judgeBudget.Overrun {
		s.judgeBudgetReason = "이전 판정의 예약 초과로 추가 호출 중단"
	}
	return nil
}

func (s *Session) recordUnfinishedJudge() {
	ev := &event.Event{Kind: event.KindTool, Tool: "judge", RawTool: "samcheonpo.judge", TS: time.Now(), Category: event.CatWatch, Basis: "live", Summary: "drift judge: cost_unknown"}
	s.parser.AddEvent(ev)
	s.eng.ObserveWatch(ev)
	s.persistEvent(ev)
}
