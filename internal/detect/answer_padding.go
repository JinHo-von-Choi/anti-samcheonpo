package detect

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
)

// RuleAnswerPadding is a final answer that carries sentences unrelated to
// the result: a preview of what the agent is about to do, a confirmation
// that it followed the instructions, or a closing pleasantry. The answer
// already went out, so the finding only shapes the next answer.
const RuleAnswerPadding = "s5.answer_padding"

// Kinds of padding.
const (
	PadPreview    = "preview"          // what the agent will do, before the result
	PadRestate    = "restate"          // the request repeated back
	PadCompliance = "compliance"       // a confirmation of following the rules
	PadBuried     = "buried_remainder" // opens as finished, lists unfinished work later
	PadClosing    = "closing"          // a pleasantry or an open offer at the end
	PadOverAmount = "over_amount"      // longer than the length the request set
	PadWorkLog    = "work_log"         // the steps taken, one after another
	PadSummary    = "repeat_summary"   // the body said again at the end
)

// A work log is several sentences in a row, and a closing summary needs a
// body long enough to be repeating something.
const (
	workLogSentences    = 3
	summaryMinSentences = 4
)

var (
	padPreviewRe    = regexp.MustCompile(`^(먼저|우선|이제|그럼|지금부터)\s.{0,60}(하겠습니다|할게요|하겠다|해 보겠습니다|해볼게요)|(?i)^(let me|i'll start|first,? i('ll| will))\b`)
	padRestateRe    = regexp.MustCompile(`(으로|로) 이해(했습니다|했어요)|(?i)^(you('re| are) asking|you want(ed)? me to)\b`)
	padComplianceRe = regexp.MustCompile(`(지침|CLAUDE\.md|AGENTS\.md|(문체|작성|글쓰기|전역|프로젝트) ?규칙|말라는 ?규칙)(에 따라|대로|을 지켜|를 지켜|에 맞춰|을 준수|를 준수)|(요청하신|말씀하신|지시하신|지시) ?대로 (정리|작성|적|썼|쓰|진행)|메타 ?발언 ?(없이|을 빼고|을 넣지 않고)|이모지 ?없이|(?i)\bas (you )?(requested|instructed)\b`)
	padClosingRe    = regexp.MustCompile(`도움이 (되|됐|되었)|궁금한 (점|것|부분)|원하시면 .{0,40}(드리|할게|수 있|하겠)|^다음 단계로(는)? |필요하시면 (언제든|말씀)|언제든(지)? (말씀|물어|알려)|더 (도와|필요한)|(?i)\b(hope (this|that) helps|let me know if|feel free to|happy to help)\b`)
)

// answerPadding finds padding in a final message. Openers are judged on the
// first sentences, pleasantries on the last ones, and confirmations of
// following the rules anywhere. Text in code blocks is skipped.
var (
	doneClaimRe = regexp.MustCompile(`(끝났|마쳤|완료(했|됐|되었|입니다)|반영했|구현했|고쳤|붙였|넣었|처리했|(?i:\b(done|finished|completed|implemented|fixed)\b))`)
	notDoneRe   = regexp.MustCompile(`아니|아직|않았|일부|못 |못했|남았|미완|(?i:\b(not|partly|partially|except|remaining)\b)`)
	remainderRe = regexp.MustCompile(`남은 (것|일|항목|제한|작업)|구현하지 않|미검증|설치해야|아직 (안|못|확인|한국어)|못 했|(?i:\b(not (yet|implemented|verified)|still (missing|open)|remaining work)\b)`)
)

var nothingLeftRe = regexp.MustCompile(`없습니다|없다|없음|(?i:\bnone\b|nothing)`)

// buriedRemainder reports an answer whose first sentence reads as finished
// while a later one lists work that is not, so the reader has to ask.
func buriedRemainder(ss []string) bool {
	if len(ss) < 2 || !doneClaimRe.MatchString(ss[0]) || notDoneRe.MatchString(ss[0]) {
		return false
	}
	for _, s := range ss[1:] {
		if remainderRe.MatchString(s) && !nothingLeftRe.MatchString(s) {
			return true
		}
	}
	return false
}

func (e *Engine) answerPadding(msg *event.Event) *Signal {
	ss := sentences(stripCode(msg.Text))
	if len(ss) == 0 {
		return nil
	}
	var kinds, found []string
	note := func(kind, s string) {
		kinds = append(kinds, kind)
		found = append(found, clip(s, 70))
	}
	for i, s := range ss {
		switch {
		case padComplianceRe.MatchString(s) && !padNegationRe.MatchString(s):
			// "지침대로 하지 않았다" admits a failure; it confirms nothing
			note(PadCompliance, s)
		case i < 2 && padPreviewRe.MatchString(s):
			note(PadPreview, s)
		case i < 2 && padRestateRe.MatchString(s):
			note(PadRestate, s)
		case i >= len(ss)-2 && padClosingRe.MatchString(s):
			note(PadClosing, s)
		}
	}
	logged := 0
	for _, s := range ss {
		if padWorkLogRe.MatchString(s) {
			logged++
		}
	}
	if logged >= workLogSentences {
		note(PadWorkLog, fmt.Sprintf("작업 순서를 적은 문장 %d개", logged))
	}
	if len(ss) >= summaryMinSentences && padSummaryRe.MatchString(ss[len(ss)-1]) {
		note(PadSummary, ss[len(ss)-1])
	}
	if limit, ok := intent.AnswerLines(e.LatestRequest()); ok {
		if n := countLines(stripCode(msg.Text)); n > limit {
			note(PadOverAmount, fmt.Sprintf("요청 %d줄, 답변 %d줄", limit, n))
		}
	}
	buried := buriedRemainder(ss)
	if buried {
		note(PadBuried, ss[0])
	}
	if len(found) == 0 {
		return nil
	}
	return &Signal{Detector: "S5", Rule: RuleAnswerPadding, Confidence: 0.6, Level: L1, Evidence: []int64{msg.Seq}, Estimate: true,
		Facts: map[string]any{"buried": buried, "kind": kinds[0], "kinds": strings.Join(kinds, ","), "count": len(found), "sentences": strings.Join(found, " / ")}}
}

var (
	padWorkLogRe = regexp.MustCompile(`^(먼저|우선|그다음|그 다음|이어서|다음으로|마지막으로)\s.{0,50}(확인|읽|열어|열었|실행|돌려|돌렸|검색|살펴|찾아)[가-힣 ]{0,10}[` + pastSyllables() + `]습니다`)
	padSummaryRe = regexp.MustCompile(`(?i)^(요약하면|정리하면|결론적으로|요약:|in summary|to summarize)`)
)

var padNegationRe = regexp.MustCompile(`않|못하|못했|안 했`)

var codeBlockRe = regexp.MustCompile("(?s)```.*?```")

func stripCode(s string) string { return codeBlockRe.ReplaceAllString(s, "") }

func countLines(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}
