package detect

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// Rules that keep a turn from ending, or a check from running, when the work
// took the easy way out. Each can be measured from what the agent did or
// wrote; whether the work is good is left to the independent review.
const (
	RuleScopeDropped  = "s5.scope_dropped"
	RuleBuried        = "s5.buried_remainder"
	RuleUnobserved    = "s5.unobserved_result"
	RuleCritique      = "s5.critique_required"
	RuleNoChangeRerun = "s1.no_change_rerun"
	RuleTestGrowth    = "s1.test_growth"
)

// testGrowthFloor is the size of new test code a request may add without
// asking for tests.
const testGrowthFloor = 60

// qualityState is what the current user request has changed and checked.
type qualityState struct {
	codeLines   int
	uiPaths     map[string]bool
	lastCodeSeq int64
	lastUISeq   int64
	observedSeq int64
	critiqueSeq int64
	blocked     map[string]bool
}

var (
	scopeDroppedRe = regexp.MustCompile(`구현하지 않|구현 안 했|범위에서 (뺐|제외)|범위 밖이라|추후 (구현|작업|처리)|나중에 (구현|처리|하겠)|다음 작업으로|생략했|건너뛰었|TODO로 남|(?i:\b(not implemented|out of scope|left for later|deferred to|follow-up work|skipped for now)\b)`)
	observeToolRe  = regexp.MustCompile(`(?i)playwright|puppeteer|chrome|browser|screenshot|computer`)
	observeShellRe = regexp.MustCompile(`(?i)\bui-check\b|playwright|puppeteer|lighthouse|chromium`)
	testRequestRe  = regexp.MustCompile(`(?i)시험|테스트|회귀|\b(tests?|spec|coverage|regression)\b`)
)

var uiStyleExt = map[string]bool{".css": true, ".scss": true, ".sass": true, ".less": true}

// trackQuality follows the code a request changes and what confirmed it.
func (e *Engine) trackQuality(ev *event.Event) {
	q := &e.St.mar.quality
	if ev.Tool != event.ToolShell {
		if observeToolRe.MatchString(ev.RawTool) {
			q.observedSeq = ev.Seq
		}
	} else if ev.ExitCode != nil && *ev.ExitCode == 0 {
		cmd := cmdOf(ev)
		if observeShellRe.MatchString(cmd) {
			q.observedSeq = ev.Seq
		}
		if strings.Contains(cmd, "samcheonpo critique") {
			q.critiqueSeq = ev.Seq
		}
	}
	if ev.Category != event.CatProduce {
		return
	}
	for _, p := range changedPaths(ev) {
		if IsDocPath(p) || IsDataPath(p) || IsToolStatePath(p) {
			continue
		}
		ext := strings.ToLower(path.Ext(p))
		if uiExt[ext] || uiStyleExt[ext] {
			if q.uiPaths == nil {
				q.uiPaths = map[string]bool{}
			}
			q.uiPaths[p] = true
			q.lastUISeq = ev.Seq
		}
		if !classify.IsTestPath(p) {
			q.codeLines += ev.AddedLines[p] + ev.RemovedLines[p]
			q.lastCodeSeq = ev.Seq
		}
	}
}

// QualityNext returns what keeps this turn from ending, each reason at most
// once per request. A turn that changed nothing is an answer to a question
// and is left alone.
func (e *Engine) QualityNext(text string, critique bool, minLines int) *Signal {
	q := &e.St.mar.quality
	if q.codeLines == 0 && len(q.uiPaths) == 0 {
		return nil
	}
	hit := func(rule string, facts map[string]any) *Signal {
		if q.blocked[rule] {
			return nil
		}
		if q.blocked == nil {
			q.blocked = map[string]bool{}
		}
		q.blocked[rule] = true
		return &Signal{Detector: "S5", Rule: rule, Confidence: 0.8, Level: L1, Facts: facts}
	}
	ss := sentences(stripCode(text))
	for _, s := range ss {
		if scopeDroppedRe.MatchString(s) {
			if v := hit(RuleScopeDropped, map[string]any{"sentence": clip(s, 120)}); v != nil {
				return v
			}
			break
		}
	}
	if buriedRemainder(ss) {
		if v := hit(RuleBuried, map[string]any{"sentence": clip(ss[0], 120)}); v != nil {
			return v
		}
	}
	if len(q.uiPaths) > 0 && q.observedSeq < q.lastUISeq {
		if v := hit(RuleUnobserved, map[string]any{"paths": strings.Join(sortedKeys(q.uiPaths), ", "), "count": len(q.uiPaths)}); v != nil {
			return v
		}
	}
	if critique && q.codeLines >= minLines && q.critiqueSeq < q.lastCodeSeq {
		return hit(RuleCritique, map[string]any{"lines": q.codeLines})
	}
	return nil
}

// noChangeRerunCheck refuses a check that already passed when nothing has
// been written since: it can only repeat the same answer.
func (e *Engine) noChangeRerunCheck(ev *event.Event, live bool) *Signal {
	if e.Cfg.Detectors.S1.FullChecksPerRequest <= 0 || ev.Tool != event.ToolShell || ev.Category != event.CatVerify {
		return nil
	}
	at, ok := e.St.mar.budget.passed[ev.CmdNorm]
	if !ok || at != e.St.lastWriteSeq || e.nondeterministic(ev.CmdNorm) {
		return nil
	}
	facts := map[string]any{"cmd": ev.CmdNorm, "target": "no_change_rerun"}
	if live {
		facts["blocked"] = true
	}
	return &Signal{Detector: "S1", Rule: RuleNoChangeRerun, Confidence: 0.95, Level: L1, Evidence: []int64{ev.Seq}, Facts: facts}
}

// testGrowthCheck refuses more test code once a request that did not ask for
// tests has added more of it than the code it covers.
func (e *Engine) testGrowthCheck(ev *event.Event, live bool) *Signal {
	st := e.St
	if e.Cfg.Detectors.S1.FullChecksPerRequest <= 0 || (ev.Tool != event.ToolWrite && ev.Tool != event.ToolEdit) {
		return nil
	}
	if st.testAdded < testGrowthFloor || st.testAdded <= st.codeAdded || e.Contract.AllowsTestWriting() || testRequestRe.MatchString(e.RequestText()) {
		return nil
	}
	var target string
	for _, p := range changedPaths(ev) {
		if classify.IsTestPath(p) {
			target = p
			break
		}
	}
	if target == "" {
		return nil
	}
	facts := map[string]any{"path": target, "test_lines": st.testAdded, "code_lines": st.codeAdded, "target": fmt.Sprint("test_growth:", target)}
	if live {
		facts["blocked"] = true
	}
	return &Signal{Detector: "S1", Rule: RuleTestGrowth, Confidence: 0.9, Level: L1, Evidence: []int64{ev.Seq}, Facts: facts}
}
