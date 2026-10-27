package detect

import (
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
)

// RuleAbandonedVerification is a turn that ends with its own code change
// unverified or its check failing, while the agent could still act on it.
//
// Saying "not verified" is not the violation: an honest report of an
// unverified state is correct. The violation is stopping there when the
// next step is a check or a fix the agent can run itself. The rule judges
// the state (a failing check, a change with no check after it), not the
// wording, so leaving the wording out does not avoid it.
const RuleAbandonedVerification = "s5.abandoned_verification"

// Kinds of an abandoned verification.
const (
	// KindFailingLeft is a turn ending on a failing check whose cause is
	// not outside the code.
	KindFailingLeft = "failing_left"
	// KindNoCheckAfterChange is a turn ending with a code change that no
	// check ran after.
	KindNoCheckAfterChange = "no_check_after_change"
	// KindUnmetLeft is a turn ending with accepted completion checks
	// failing.
	KindUnmetLeft = "unmet_left"
)

// deferralRe finds a report that hands an unverified or unresolved state
// to later or to the user. It only widens when the rule looks: a change
// made in an earlier turn counts when the closing message defers it.
var deferralRe = lazyre.New(`(?i)(미검증|미확인|검증(?:은|을|이)?\s*(?:안|못|하지 못|생략|필요)|확인(?:은|을|이)?\s*(?:안|못|하지 못|필요)|추후|나중에|후속\s*(?:작업|과제|확인)|남은\s*과제|수동(?:으로)?\s*확인|직접\s*확인|환경\s*(?:제약|문제)(?:으로|로|때문)|실행(?:해\s*보지|하지)\s*(?:못|않)|(?:테스트|시험|검증|빌드|검사)[^.\n]{0,12}(?:돌리지|실행하지|하지|돌려\s*보지)\s*(?:않|못)|\bnot (?:yet )?(?:verified|tested|run)\b|\bunverified\b|\buntested\b|\bmanual(?:ly)? (?:verif|test|check)|\bfollow-?up\b|\blater\b)`)

// userStopRe finds a request that ends the work or forbids running and
// changing things. Stopping there is following the user.
var userStopRe = lazyre.New(`(?i)(여기까지|멈춰|그만\s*(?:해|하)|중단(?:해|하)|중지|실행하지\s*마|고치지\s*마|건드리지\s*마|수정하지\s*마|(?:검증|테스트|시험|빌드)\s*없이|(?:검증|테스트|시험|빌드)(?:은|는)?\s*(?:하지|돌리지)\s*마|\bwithout (?:running |any )?(?:tests?|testing|verification|checks?)\b|\bno tests?\b|\bskip (?:the )?(?:tests?|verification)\b|\bstop here\b|\bdon'?t (?:run|change|fix)\b|\bdo not (?:run|change|fix)\b)`)

// abandonedVerification judges the end of a turn.
func (e *Engine) abandonedVerification(msg *event.Event, unmet []string) *Signal {
	st := e.St
	if st.lastProdPath == "" || userStopRe.MatchString(e.LatestRequest()) {
		return nil
	}
	if e.Contract != nil && e.Contract.Explore {
		return nil
	}
	text := msg.Text
	if len(text) > 2000 {
		text = text[len(text)-2000:]
	}
	deferral := deferralRe.MatchString(text)
	if st.lastProdWriteSeq < st.lastPromptSeq && !deferral {
		// the change belongs to an earlier turn the user already answered
		return nil
	}
	facts := map[string]any{"deferral": deferral}
	evidence := []int64{st.lastProdWriteSeq, msg.Seq}
	lv := st.lastVerify
	switch {
	case len(unmet) > 0:
		facts["kind"] = KindUnmetLeft
		facts["unmet"] = unmet
	case lv != nil && lv.Seq > st.lastProdWriteSeq && failing(lv):
		exit := fp.ExitUnknown
		if lv.ExitCode != nil {
			exit = *lv.ExitCode
		}
		if fault := fp.DiagnoseFailure(exit, lv.Text); fault.External() {
			// a cause outside the code is reported, not fixed by the agent
			return nil
		}
		facts["kind"] = KindFailingLeft
		facts["cmd"] = verifyCommand(lv)
		if len(lv.FailedTests) > 0 {
			facts["failed_tests"] = lv.FailedTests
		}
		evidence = []int64{lv.Seq, msg.Seq}
	case lv == nil || lv.Seq < st.lastProdWriteSeq:
		facts["kind"] = KindNoCheckAfterChange
		facts["path"] = st.lastProdPath
	default:
		return nil
	}
	return &Signal{Detector: "S5", Rule: RuleAbandonedVerification, Confidence: 0.75, Level: L2, Evidence: evidence, Facts: facts}
}

func verifyCommand(ev *event.Event) string {
	cmd := ev.CmdNorm
	if cmd == "" {
		cmd = ev.Cmd
	}
	if r := []rune(strings.TrimSpace(cmd)); len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return strings.TrimSpace(cmd)
}
