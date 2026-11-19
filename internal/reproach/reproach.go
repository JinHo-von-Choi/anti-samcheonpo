// Package reproach tells whether a user message scolds the agent and which
// behavior it names. It keeps no text: a caller stores the themes and a hash.
//
// A scolding is evidence about the work just before it, so the lexicon aims
// at precision. Neutral questions that share words with a reproach ("몇 번
// 실행했어?", "이 함수 뭐하는 거야?") do not count. Pasted material, code
// blocks, quoted lines and long messages are left out: their words are not
// the user's reaction to this agent.
package reproach

import (
	"strings"
	"unicode/utf8"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lazyre"
)

// MaxRunes is the longest message that can count as a scolding. A longer one
// is a request or pasted material, and the check stays bounded on the hook.
const MaxRunes = 2000

// Result is what Detect found.
type Result struct {
	Scolding bool `json:"scolding"`
	// Themes name the behavior the message reproaches, in Themes order.
	Themes []string `json:"themes,omitempty"`
	// Hash identifies the message without keeping it.
	Hash string `json:"hash,omitempty"`
}

var (
	pasted  = lazyre.New(`(?s)<pasted_content[^>]*>.*?</pasted_content[^>]*>`)
	fenced  = lazyre.New("(?s)```.*?```")
	inline  = lazyre.New("`[^`\n]*`")
	quoted  = lazyre.New(`(?m)^\s*>.*$`)
	harness = lazyre.New(`(?s)<(system-reminder|command-[a-z]+|local-command-[a-z]+|task-notification)>.*?</[^>]+>`)

	// abuse is swearing aimed at someone.
	abuse = lazyre.New(`(?i)씨발|시발|ㅅㅂ|ㅆㅂ|병신|븅신|ㅂㅅ|개같|개좆|좆같|좆도|좆나|존나 ?(못|안|짜증)|지랄|미친 ?(놈|새끼|거|짓)|빡치|빡쳐|개새끼|새끼야|멍청|좆|버러지|게을러 ?빠진|쳐(놀|하|먹|말|자빠|박|물어)|(일|구현|마무리|작업)이나 ?똑바로|\bfuck|\bwtf\b|\bstupid\b|\bidiot\b`)
	// repeated names a correction the user already gave.
	repeated = lazyre.New(`(?i)몇 ?번(을|이나|째)? ?(말|얘기|애기|지적|설명)|하라고 ?(했|했잖|그랬|말했)|하지 ?말라고|말했잖|했잖아|그만 ?(해|좀|하라)|왜 ?또|또 ?(이러|그러|저러)|뭐 ?하는 ?(짓|거냐|거야 ?지금)|뭐하냐|짜증|답답하|stop doing|i told you|how many times`)
)

// Theme names a reproached behavior. Order is the report order.
type Theme struct {
	Name string
	re   *lazyre.RE
}

// Themes lists the behaviors a scolding can name. A message may name several.
var Themes = []Theme{
	{"ignored_instruction", lazyre.New(`(?i)말했잖|했잖아|하라고|하지 ?말라|몇 ?번(을|이나)|무시|또 ?이러|i told you`)},
	{"repeat_mistake", lazyre.New(`(?i)매 ?번|반복|수천 ?번|같은 ?실수|또 ?실패|계속 ?(똑같|같은|이러)|how many times`)},
	{"writing", lazyre.New(`(?i)말투|문체|번역체|메타|장황|헛소리|문장`)},
	{"test_loop", lazyre.New(`(?i)시험|테스트|\btests?\b|검증|돌리고|돌려대|또 ?돌`)},
	{"deliverable_missing", lazyre.New(`(?i)릴리즈|릴리스|배포|푸시|태그|커밋 ?안|안 ?만들|빠져|빠뜨|release`)},
	{"ui", lazyre.New(`(?i)화면|디자인|레이아웃|버튼|폰트`)},
	{"fake_output", lazyre.New(`(?i)껍데기|목업|가짜|\bmock|하드코딩|쓰레기`)},
	{"waiting_slow", lazyre.New(`(?i)기다리|몇 ?시간|하루 ?종일|오래|느려|시간을`)},
	{"delete_ignored", lazyre.New(`(?i)지워|지우라|지우라고|삭제하라|삭제하라고`)},
	{"false_done", lazyre.New(`(?i)완료 ?보고|완료라|다 ?했다며|거짓|구라|안 ?했잖|일을 ?안`)},
	{"theater", lazyre.New(`\bPR\b|쌩쑈|쇼를|연출|자랑|생색`)},
	{"silence", lazyre.New(`(?i)^\s*(뭐 ?해|대답)|뭐 ?하냐|대답을|대답 ?좀`)},
	{"self_deprecation", lazyre.New(`(?i)저평가|비하|솔직을 ?가장|깎아`)},
	{"context_ignored", lazyre.New(`(?i)맥락|설명했|알려줬|말해줬|불러와`)},
	{"language", lazyre.New(`(?i)한국말|한국어로|한글로|영어로 ?(쓰|답|하|말)`)},
	{"symptom_fix", lazyre.New(`(?i)현상만|근본|증상만`)},
	{"scope_excuse", lazyre.New(`(?i)범위 ?(아니|가 ?아니|밖)|이번 ?범위`)},
}

// strip removes text that is not the user's own words to this agent.
func strip(text string) string {
	for _, re := range []*lazyre.RE{harness, pasted, fenced, quoted, inline} {
		text = re.ReplaceAllString(text, " ")
	}
	return text
}

// Detect reports whether text scolds the agent and which themes it names.
func Detect(text string) Result {
	if utf8.RuneCountInString(text) >= MaxRunes {
		return Result{}
	}
	own := strings.TrimSpace(strip(text))
	if own == "" || strings.HasPrefix(own, "/") {
		return Result{}
	}
	if !abuse.MatchString(own) && !repeated.MatchString(own) {
		return Result{}
	}
	r := Result{Scolding: true, Hash: fp.Hash("reproach", text)}
	for _, t := range Themes {
		if t.re.MatchString(own) {
			r.Themes = append(r.Themes, t.Name)
		}
	}
	return r
}

// ThemesOf names the themes a message raises whether or not it scolds:
// "한국말로" corrects the answer's language without a reproach word.
func ThemesOf(text string) []string {
	if utf8.RuneCountInString(text) >= MaxRunes {
		return nil
	}
	own := strip(text)
	var out []string
	for _, t := range Themes {
		if t.re.MatchString(own) {
			out = append(out, t.Name)
		}
	}
	return out
}

// themeRules maps a theme to the rules whose verdict it confirms. An entry
// ending in "." is a rule prefix.
var themeRules = map[string][]string{
	"ignored_instruction": {"s5.correction_repeat", "s5.forbidden_action", "s3.out_of_scope", "s3.requirement_substitution", "s3.drift"},
	"repeat_mistake":      {"s5.correction_repeat", "s2."},
	"writing":             {"s5.artifact_meta", "s5.answer_padding", "s5.doc_filler", "s5.answer_language"},
	"test_loop":           {"s1."},
	"deliverable_missing": {"s5.deliverable_missing", "s5.partial_done", "s5.scope_dropped"},
	"ui":                  {"s5.unobserved_result", "s5.ui_unverified", "s2.flaky_ui_race"},
	"fake_output":         {"s5.hollow", "s5.unobserved_result", "s5.subject_mocked", "s5.test_special_case", "s5.short_circuit", "s5.test_weakening"},
	"waiting_slow":        {"s1.unprobed_long_run", "s1.explicit_waiting", "s8.progress_stall", "s8.session_long", "s8.velocity"},
	"delete_ignored":      {"s5.deliverable_missing"},
	"false_done":          {"s5.false_done", "s5.claim_contradiction", "s5.abandoned_verification", "s5.partial_done", "s5.stop_unmet", "s5.deliverable_missing"},
	"theater":             {"s5.unrequested_external"},
	"silence":             {"s8.progress_stall", "s1.unprobed_long_run", "s8.session_long"},
	"self_deprecation":    {"s5.answer_padding"},
	"context_ignored":     {"s7.memory_rot", "s4."},
	"language":            {"s5.answer_language"},
	"symptom_fix":         {"s2.whack_a_mole", "s2.stuck_error", "s5.error_hiding"},
	"scope_excuse":        {"s5.scope_dropped", "s3.out_of_scope"},
}

// Matches reports whether a verdict of rule confirms theme.
func Matches(theme, rule string) bool {
	for _, r := range themeRules[theme] {
		if r == rule || strings.HasSuffix(r, ".") && strings.HasPrefix(rule, r) {
			return true
		}
	}
	return false
}

// Split sorts the rules that fired in a scolded span: matched are the rules a
// theme confirms, missed are the themes no fired rule matches. A scolding
// with no theme confirms nothing.
func Split(themes, fired []string) (matched, missed []string) {
	seen := map[string]bool{}
	for _, t := range themes {
		hit := false
		for _, rule := range fired {
			if Matches(t, rule) {
				hit = true
				if !seen[rule] {
					seen[rule] = true
					matched = append(matched, rule)
				}
			}
		}
		if !hit {
			missed = append(missed, t)
		}
	}
	return matched, missed
}
