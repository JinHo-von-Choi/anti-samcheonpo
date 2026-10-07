package live

import (
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestLiveSummaryAndStatusDoNotInterpretMissingUsageAsFree(t *testing.T) {
	s, _ := reliabilitySession(t)
	for _, text := range []string{s.Statusline(), s.plainSummary()} {
		if strings.Contains(text, "0원") || !strings.Contains(text, "미확인") {
			t.Fatal(text)
		}
	}
	s.mu.Lock()
	s.eng.St.Events = append(s.eng.St.Events, &event.Event{Seq: 1, Usage: event.Usage{In: 10}, Priced: true, CostMicroKRW: 1000000})
	s.eng.St.TotalMicro = 1000000
	s.mu.Unlock()
	if text := s.plainSummary(); !strings.Contains(text, "API 환산액") || strings.Contains(text, "쓴 돈:") {
		t.Fatal(text)
	}
}
