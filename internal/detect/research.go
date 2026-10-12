package detect

import (
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/research"
)

func (e *Engine) observeResearchProgress(ev *event.Event) {
	if !intent.ResearchRequest(e.FirstPrompt) {
		e.Research = nil
		return
	}
	scope := fp.Hash(e.FirstPrompt)
	if r := ev.Reliability; r != nil {
		scope = fp.Hash(scope, r.TaskID, fmt.Sprint(r.Revision))
	} else if e.Research != nil && e.Research.Goal == e.FirstPrompt {
		// Messages do not carry tool reliability metadata. Missing metadata is
		// not a new task revision and must not discard previously read sources.
		scope = e.Research.Scope
	}
	if e.Research == nil || e.Research.Scope != scope {
		e.Research = research.New(e.FirstPrompt, scope)
	}
	e.Research.Observe(ev)
}
