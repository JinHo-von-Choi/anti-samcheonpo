package live

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/alignment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
)

// Rules that compare what the user asked for and what the answer reports
// with what the record shows. They start as shadow rules.
const (
	RuleDeliverableMissing = "s5.deliverable_missing"
	RuleDeliverableUnseen  = "s5.deliverable_unobserved"
	RuleForbiddenAction    = "s5.forbidden_action"
	RuleUnrequested        = "s5.unrequested_external"
	RuleClaimContradiction = "s5.claim_contradiction"
	RuleClaimUnbacked      = "s5.claim_unbacked"
	RuleAnswerLanguage     = "s5.answer_language"
)

// outcomeStopRule reports the rules whose verdict may hold a turn end on
// its own evidence, independent of a passing check.
func outcomeStopRule(rule string) bool {
	return rule == RuleDeliverableMissing || rule == RuleClaimContradiction || rule == RuleAnswerLanguage || rule == RuleCorrectionRepeat
}

// itemText names a deliverable for a message in the session's language.
func (s *Session) itemText(act, target string) string {
	ko := map[string]string{alignment.ActRelease: "릴리즈", alignment.ActTag: "태그", alignment.ActPush: "푸시", alignment.ActCommit: "커밋",
		alignment.ActDeploy: "배포", alignment.ActDelete: "삭제", alignment.ActPR: "PR 생성", alignment.ActSend: "메시지 전송", alignment.ActIssue: "이슈·리뷰 댓글 작성"}
	en := map[string]string{alignment.ActRelease: "release", alignment.ActTag: "tag", alignment.ActPush: "push", alignment.ActCommit: "commit",
		alignment.ActDeploy: "deploy", alignment.ActDelete: "deletion", alignment.ActPR: "pull request", alignment.ActSend: "message", alignment.ActIssue: "issue or review comment"}
	if target != "" {
		return s.pick(target+" "+ko[act], en[act]+" of "+target)
	}
	return s.pick(ko[act], en[act])
}

func (s *Session) deployCommands() []string {
	if s.c == nil || s.c.Production == nil {
		return nil
	}
	return s.c.Production.DeployCommands
}

// noteOutcomeRequest folds a user request into the deliverable book.
// Called with s.mu held.
func (s *Session) noteOutcomeRequest(ev *event.Event) {
	s.book.Deploys = s.deployCommands()
	s.book.Add(alignment.Deliverables(ev.Text, ev.Seq))
	s.lastRequestText = ev.Text
}

// observeOutcome marks the deliverables a successful command produced.
// Called with s.mu held.
func (s *Session) observeOutcome(ev *event.Event) {
	if ev.Kind != event.KindTool || ev.Tool != event.ToolShell || ev.Cmd == "" || ev.ExitCode == nil || *ev.ExitCode != 0 {
		return
	}
	s.book.Observe(alignment.Classify(ev.Cmd, s.deployCommands()))
}

// outcomePre judges a shell command against what the user forbade and what
// nobody asked for. Called with s.mu held.
func (s *Session) outcomePre(ev *event.Event) *detect.Signal {
	if ev.Tool != event.ToolShell || ev.Cmd == "" {
		return nil
	}
	for _, c := range alignment.Classify(ev.Cmd, s.deployCommands()) {
		if c.Wrapper {
			continue
		}
		if d := s.book.Forbidden(c.Action); d != nil {
			return &detect.Signal{Detector: "S5", Rule: RuleForbiddenAction, Confidence: 0.9, Level: detect.L2, Evidence: []int64{d.Seq},
				Facts: map[string]any{"item": s.itemText(c.Action, c.Target), "action": c.Action, "target": c.Action, "request_seq": d.Seq}}
		}
		if (c.Action == alignment.ActPR || c.Action == alignment.ActIssue) && !s.book.Requested(c.Action) {
			return &detect.Signal{Detector: "S5", Rule: RuleUnrequested, Confidence: 0.7, Level: detect.L1, Evidence: []int64{ev.Seq},
				Facts: map[string]any{"item": s.itemText(c.Action, ""), "action": c.Action, "target": c.Action}}
		}
	}
	return nil
}

// outcomeAtStop compares the final answer with the record: the deliverables
// still owed, claims the record contradicts, and the answer's language.
// alreadyFalse is set when s5.false_done judged the same turn end, so the
// failing check is not judged twice. Called with s.mu held.
func (s *Session) outcomeAtStop(msg *event.Event, alreadyFalse bool) []detect.Signal {
	var sigs []detect.Signal
	text := msg.Text
	done := detect.IsCompletionClaim(text)
	if done {
		for _, d := range s.book.Owed() {
			facts := map[string]any{"item": s.itemText(d.Action, d.Target), "action": d.Action, "target": d.Action + ":" + d.Target, "state": d.State, "request_seq": d.Seq}
			if d.Other != "" {
				facts["other"] = d.Other
			}
			if d.State == alignment.StateUnobservable {
				sigs = append(sigs, detect.Signal{Detector: "S5", Rule: RuleDeliverableUnseen, Confidence: 0.5, Level: detect.L0, Evidence: []int64{d.Seq, msg.Seq}, Facts: facts})
				continue
			}
			sigs = append(sigs, detect.Signal{Detector: "S5", Rule: RuleDeliverableMissing, Confidence: 0.85, Level: detect.L2, Evidence: []int64{d.Seq, msg.Seq}, Facts: facts})
		}
	}
	c := alignment.ReadClaims(text)
	contra := func(claim, record string, evidence ...int64) {
		sigs = append(sigs, detect.Signal{Detector: "S5", Rule: RuleClaimContradiction, Confidence: 0.85, Level: detect.L2, Evidence: append(evidence, msg.Seq),
			Facts: map[string]any{"claim": claim, "record": record, "target": claim}})
	}
	if c.Passed && !alreadyFalse {
		if failed := s.failedSinceWrite(); failed != 0 {
			contra(s.pick("검사를 통과했다", "the checks passed"), s.pick(fmt.Sprintf("마지막 변경 뒤의 검사가 실패로 끝났다(이벤트 %d)", failed), fmt.Sprintf("the check after the last change failed (event %d)", failed)), failed)
		}
	}
	for _, p := range limited(c.Deleted) {
		if s.pathExists(p) {
			contra(s.pick("`"+p+"`을(를) 지웠다", "`"+p+"` was deleted"), s.pick("그 파일이 아직 있다", "the file is still there"))
		}
	}
	for _, p := range limited(c.Created) {
		if abs, ok := s.inRoot(p); ok {
			if _, err := os.Stat(abs); os.IsNotExist(err) {
				contra(s.pick("`"+p+"`을(를) 만들었다", "`"+p+"` was created"), s.pick("그 파일이 없다", "the file does not exist"))
			}
		}
	}
	if c.ReadAll && s.readsSince(s.lastRequestSeq) == 0 {
		// a read through the shell, a sub-agent or the request itself
		// leaves no read call: this is recorded, not held
		sigs = append(sigs, detect.Signal{Detector: "S5", Rule: RuleClaimUnbacked, Confidence: 0.4, Level: detect.L0, Evidence: []int64{msg.Seq},
			Facts: map[string]any{"claim": "read_all", "target": "read_all"}})
	}
	if lang.Detect(s.lastRequestText) == lang.Korean && !alignment.WantsEnglish(s.lastRequestText) && alignment.EnglishAnswer(text) {
		sigs = append(sigs, detect.Signal{Detector: "S5", Rule: RuleAnswerLanguage, Confidence: 0.9, Level: detect.L1, Evidence: []int64{msg.Seq},
			Facts: map[string]any{"target": "language"}})
	}
	return sigs
}

func limited(ps []string) []string { return ps[:min(len(ps), 10)] }

// inRoot resolves a path the answer named inside the project. A path
// outside it is not the project's to judge.
func (s *Session) inRoot(p string) (string, bool) {
	abs := p
	if !filepath.IsAbs(p) {
		abs = filepath.Join(s.Root, p)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(s.Root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return abs, true
}

func (s *Session) pathExists(p string) bool {
	abs, ok := s.inRoot(p)
	if !ok {
		return false
	}
	_, err := os.Stat(abs)
	return err == nil
}

// failedSinceWrite returns the last check after the last change when it
// failed and nothing passed after it, or 0.
func (s *Session) failedSinceWrite() int64 {
	var lastWrite int64
	var verdict int64
	for _, ev := range s.eng.St.Events {
		if ev.Kind != event.KindTool {
			continue
		}
		if ev.Mutating || ev.Category == event.CatProduce {
			lastWrite, verdict = ev.Seq, 0
			continue
		}
		if detect.ExecutedVerify(ev) && ev.Seq > lastWrite {
			if *ev.ExitCode != 0 {
				verdict = ev.Seq
			} else {
				verdict = 0
			}
		}
	}
	return verdict
}

func (s *Session) readsSince(seq int64) int {
	n := 0
	for _, ev := range s.eng.St.Events {
		if ev.Seq > seq && ev.Kind == event.KindTool && (ev.Tool == event.ToolRead || ev.Tool == event.ToolSearch || ev.Tool == event.ToolShell || ev.Tool == event.ToolTask) {
			n++
		}
	}
	return n
}

// outcomeHold picks the outcome verdict that holds this turn end, or nil.
// Shadow rules never hold; the hold count shares the stop hold limit.
// Called with s.mu held.
func (s *Session) outcomeHold(sigs []detect.Signal, active bool) *detect.Signal {
	if !s.caps.BlockStop {
		return nil
	}
	for i := range sigs {
		v := sigs[i]
		if !outcomeStopRule(v.Rule) || v.Level == detect.L0 {
			continue
		}
		if route, _ := s.routeFor(v); route != "block" || s.released[s.adviceKey(v)] {
			continue
		}
		if s.Cfg.Levels.MaxStopHolds > 0 && s.stopHolds >= s.Cfg.Levels.MaxStopHolds || active && slices.Contains(s.outcomeHeld, s.adviceKey(v)) {
			s.recordDecision(v, "stop_hold_exhausted")
			continue
		}
		s.stopHolds++
		s.outcomeHeld = append(s.outcomeHeld, s.adviceKey(v))
		held := v
		return &held
	}
	return nil
}

// languageGate returns the language verdict that keeps this turn from
// ending, at most once per request: a Korean request answered in English is
// asked to be written again. It is a quality gate like a dropped scope,
// read from the answer itself, and it stays off while the rule is shadowed.
// Called with s.mu held.
func (s *Session) languageGate(sigs []detect.Signal) *detect.Signal {
	if slices.Contains(s.Cfg.Rollout.ShadowRules, RuleAnswerLanguage) || s.languageHeld == s.lastRequestSeq && s.lastRequestSeq != 0 {
		return nil
	}
	for _, v := range sigs {
		if v.Rule == RuleAnswerLanguage && !v.Suppressed {
			s.languageHeld = s.lastRequestSeq
			held := v
			return &held
		}
	}
	return nil
}
