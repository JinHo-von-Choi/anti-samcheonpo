package live

import (
	"fmt"
	"slices"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// queueTrimPass asks, once per document and request, for the delete-only pass
// over what this turn wrote. It runs in a context other than the one that
// wrote the text, because a writer judges its own text kindly. The caller
// holds s.mu.
func (s *Session) queueTrimPass() {
	if !s.Cfg.Trim.Enabled {
		return
	}
	var paths []string
	for _, ev := range s.eng.St.Events {
		if ev.Seq <= s.trimThrough || ev.Tool != event.ToolWrite && ev.Tool != event.ToolEdit || ev.IsError {
			continue
		}
		for _, p := range ev.Paths {
			if n, done := s.trimDone[p]; detect.ReaderDocument(p) && (!done || n != len(s.eng.Requests)) && !slices.Contains(paths, p) {
				paths = append(paths, p)
			}
		}
	}
	if n := len(s.eng.St.Events); n > 0 {
		s.trimThrough = s.eng.St.Events[n-1].Seq
	}
	if len(paths) == 0 {
		return
	}
	if s.trimDone == nil {
		s.trimDone = map[string]int{}
	}
	for _, p := range paths {
		s.trimDone[p] = len(s.eng.Requests)
	}
	s.pending = append(s.pending, fmt.Sprintf(s.pick("[삼천포] 덜어내기: 이번에 쓴 문서 %s의 사본을 새 세션이나 하위 에이전트에 넘겨 \"지워도 목적이 유지되는 문단과 요소를 지운다\"는 작업만 한 번 실행한다. 결과는 `samcheonpo trim --apply <원본> <결과>`로 적용한다. 결과에 새 낱말이 있으면 원본이 그대로 남는다.", "[samcheonpo] Trim: give a copy of the document %s you just wrote to a new session or subagent and run one pass that only \"deletes paragraphs and elements whose removal keeps the purpose\". Apply the result with `samcheonpo trim --apply <original> <result>`. If the result has any new word, the original stays as it is."), strings.Join(paths, ", ")))
}
