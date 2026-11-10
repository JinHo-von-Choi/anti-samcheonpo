package detect

import (
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Kinds of a sentence in a shared artifact that talks about how the work was
// done instead of what the artifact says.
const (
	// MetaWorkReport tells what the writer did: "~를 정리했다", "검토를
	// 반영했다". A sentence that says what a feature does is not one: the
	// same verb in the present tense ("중복 항목을 정리한다") describes the
	// thing, so only completed forms count.
	MetaWorkReport = "work_report"
	// MetaRequestRef points at the request, the instructions or a review.
	MetaRequestRef = "request_ref"
	// MetaSelfRef points at the work itself: this session, this change, the
	// document describing itself.
	MetaSelfRef = "self_reference"
	// MetaVerifyLog records how the result was checked.
	MetaVerifyLog = "verification_log"
)

// MetaHit is one sentence that talks about the work.
type MetaHit struct {
	Kind     string `json:"kind"`
	Sentence string `json:"sentence"`
	Path     string `json:"path,omitempty"`
	// Fix is the sentence rewritten to say what the subject does; empty
	// means the sentence can simply be deleted.
	Fix string `json:"fix,omitempty"`
}

var (
	metaLeadRe = regexp.MustCompile(`^(?:(?:요청|지시|지적|피드백|리뷰 ?의견|검토 ?의견|코멘트|말씀)(?:에 따라|대로|을 반영해|를 반영해|을 받아|를 받아|에 맞춰)\s*|(?i:as (?:you )?requested,?\s*|per (?:the|your) (?:request|feedback|review|instructions?),?\s*|(?:based on|following) (?:the )?(?:feedback|review),?\s*))`)
	metaPastRe = []struct {
		re *regexp.Regexp
		to string
	}{
		{regexp.MustCompile(`(했습니다|했다|하였다|했음|함)\.?$`), "한다."},
		{regexp.MustCompile(`(됐습니다|됐다|되었다|됨)\.?$`), "된다."},
	}
)

// metaRewrite turns a sentence that opens with the request it followed into
// one that says what changed. It returns "" when nothing would be left but
// the report itself, and the sentence is then a deletion candidate.
func metaRewrite(s string) string {
	if strings.HasSuffix(s, "…") || !metaLeadRe.MatchString(s) {
		return ""
	}
	body := metaLeadRe.ReplaceAllString(s, "")
	for _, p := range metaPastRe {
		body = p.re.ReplaceAllString(body, p.to)
	}
	if utf8.RuneCountInString(body) < 6 || len(MetaSentences(body)) > 0 {
		return ""
	}
	if r := []rune(body); r[0] >= 'a' && r[0] <= 'z' {
		r[0] -= 'a' - 'A'
		body = string(r)
	}
	return body
}

// completedRe is a sentence that reports something done: a past form (a
// syllable ending in ㅆ before 다, 고, 습니다 ...) or a change noun at its
// end. A rule with it describes past work; without it the same words
// describe what a feature does ("지시대로 값을 채운다").
var completedRe = regexp.MustCompile(`[` + pastSyllables() + `](다|고|습니다|으며|는데|어요|음)|(함|됨|완료|추가|수정|변경|반영|정리|삭제)[^가-힣]*$`)

// pastSyllables lists the Hangul syllables whose final consonant is ㅆ, the
// past-tense marker (했, 됐, 썼, 쳤 ...).
func pastSyllables() string {
	var b strings.Builder
	for r := rune(0xAC00); r <= 0xD7A3; r++ {
		if (r-0xAC00)%28 == 20 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// thirdPartyRe is a sentence whose topic is someone or something other than
// the writer: "ACT-R은 이를 정리했다" reports another's work.
var thirdPartyRe = regexp.MustCompile(`[가-힣A-Za-z0-9)\]]+(은|는)\s`)

var writerTopicRe = regexp.MustCompile(`(^|\s)(저는|제가|우리는|저희는|나는|내가)\s`)

var metaRules = []struct {
	kind string
	re   *regexp.Regexp
	also *regexp.Regexp // when set, the sentence has to match it too
}{
	{MetaRequestRef, regexp.MustCompile(`(요청|지시|지적|피드백|리뷰 ?의견|검토 ?의견|코멘트|말씀)(에 따라|대로|을 반영|를 반영|에 맞춰|을 받아|를 받아)|요청하신|말씀하신`), completedRe},
	{MetaRequestRef, regexp.MustCompile(`(?i)\b(as (you )?requested|per (the|your) (request|feedback|review|instructions?)|address(ed|es|ing)? (the )?(review|feedback|comments?)|(based on|following) (the )?(feedback|review))\b`), nil},
	{MetaSelfRef, regexp.MustCompile(`이번\s?(작업|세션|대화|변경|커밋|수정|릴리[스즈]|PR)(에서|으로|에|은|는|을|를)`), completedRe},
	{MetaSelfRef, regexp.MustCompile(`(정리|요약|모아 ?둔|기록)한 (표|문서|글|목록|절)(다|이다|입니다)|이 (문서|글|표|절)(는|은) .{0,40}(정리|요약|기록)(한|했)`), nil},
	{MetaVerifyLog, regexp.MustCompile(`(검증|시험|테스트|검사|확인) ?(결과|과정)(를|을)? ?(반영|거쳐)|\d+\s?(개|건|/\d+)\s?(모두 |전부 )?통과(했|함|하였)|^\d{4}-\d{2}-\d{2}\.?\s*(실행|검증|실험|확인|재실행)`), completedRe},
	{MetaSelfRef, regexp.MustCompile(`(?i)^(in|during) (this|the current) (session|conversation)\b|\bas discussed\b`), nil},
	{MetaWorkReport, regexp.MustCompile(`(?i)^(i|we) (have |'ve )?(updated|cleaned|reviewed|verified|checked|refactored|fixed|added|removed|changed|tidied)\b|\bincorporat(ed|es|ing) (the )?(feedback|review|comments?|suggestions?)\b|\bapplied (the )?(review|feedback|suggestions?)\b|\b(after|following) code review\b`), nil},
	{MetaVerifyLog, regexp.MustCompile(`(?i)\btests? (now )?passed\b|\ball (the )?tests (now )?pass\b|\btests are (now )?passing\b|\bverified (that|locally)\b`), nil},
	{MetaWorkReport, regexp.MustCompile(`(의견|피드백|리뷰|검토|지적|요청|지시|코멘트|결과)(을|를|은|는)? ?반영\s?(했|하였|함(?:$|[\s.,)])|했음|완료)|(정리|현행화|재검토|검토|재검증|검증|확인|점검|보강|개선|손질)\s?(했|하였|해 ?두었|해 ?뒀|함(?:$|[\s.,)])|했음|완료)|(다듬었|걷어냈|덜어냈|손봤|고쳐 ?두었)|(?i)^\s*(i|we)( have|'ve)? (cleaned|tidied|verified|checked|reviewed|polished|double-checked)\b`), nil},
}

// MetaSentences returns the sentences of a text that talk about the work:
// what the writer did, the request it followed, how it was checked, or the
// tool that wrote it. The judgment is about the sentence's subject, so a
// verb that also names what a feature does counts only in its completed form.
func MetaSentences(text string) []MetaHit {
	var out []MetaHit
	for _, s := range sentences(text) {
		// a condition ("확인했다면", "검토했으면") tells the reader when to do
		// something; it reports nothing
		probe := conditionalRe.ReplaceAllString(quotedRe.ReplaceAllString(s, " "), "")
		for _, r := range metaRules {
			if r.also != nil && !r.also.MatchString(probe) {
				continue
			}
			if r.kind == MetaWorkReport && thirdPartyRe.MatchString(probe) && !writerTopicRe.MatchString(probe) {
				continue
			}
			if r.re.MatchString(probe) {
				out = append(out, MetaHit{Kind: r.kind, Sentence: clip(s, 90), Fix: metaRewrite(s)})
				break
			}
		}
	}
	return out
}

var (
	// a quoted phrase is an example being discussed, not the writer's
	// own sentence
	quotedRe      = regexp.MustCompile("\"[^\"]*\"|“[^”]*”|‘[^’]*’|「[^」]*」|`[^`]*`")
	conditionalRe = regexp.MustCompile(`(했|하였)(다면|으면|더라도|는지|던|을 ?때)`)
	listMarkRe    = regexp.MustCompile(`^\s*(?:[-*+>]|#{1,6}|\d+[.)])\s+`)
	sentenceEnd   = regexp.MustCompile(`([.!?。])\s+`)
)

// sentences splits text into lines and the lines into sentences, with list
// and heading marks removed.
func sentences(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(listMarkRe.ReplaceAllString(line, ""))
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		for _, s := range strings.Split(sentenceEnd.ReplaceAllString(line, "$1\n"), "\n") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// metaExempt names files that are notes for the agent or the operator, not
// something shared with readers: plans, memory, scratch files and the
// instruction files agents read.
func metaExempt(p string) bool {
	p = strings.ToLower(path.Clean(strings.ReplaceAll(p, "\\", "/")))
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "../") {
		return true // outside the workspace
	}
	switch base := path.Base(p); {
	case base == "claude.md" || base == "agents.md" || base == "gemini.md" || base == "memory.md":
		return true
	case strings.Contains(base, "plan") || strings.Contains(base, "플랜") || strings.Contains(base, "계획"):
		// a plan is a working note, wherever it is kept
		return true
	}
	for _, seg := range strings.Split(path.Dir(p), "/") {
		switch seg {
		case "plans", "notes", "scratch", "tmp", "memory", "benchmarks", ".claude", ".samcheonpo", ".codex", ".cursor":
			return true
		}
	}
	return false
}

var commentLineRe = regexp.MustCompile(`^\s*(?://+|/\*+|\*+(?:\s|$)|#+(?:\s|$)|--\s|<!--|;+\s)(.*?)(?:\*/|-->)?\s*$`)

// codeCommentExt lists source files whose comments are read.
var codeCommentExt = map[string]bool{".go": true, ".rs": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".py": true, ".rb": true,
	".java": true, ".kt": true, ".swift": true, ".c": true, ".h": true, ".cc": true, ".cpp": true, ".cs": true, ".php": true, ".sh": true, ".sql": true,
	".lua": true, ".vue": true, ".svelte": true, ".css": true, ".scss": true, ".html": true, ".yml": true, ".yaml": true, ".toml": true, ".gd": true}

// SharedText returns the reader-facing text a write adds to a file: the
// added lines of a document, or the comments among the added lines of a
// source file. Notes for agents and files outside the workspace return "".
func SharedText(p string, added []string) string {
	if metaExempt(p) {
		return ""
	}
	if IsDocPath(p) || strings.HasPrefix(strings.ToLower(path.Base(p)), "readme") || strings.HasPrefix(strings.ToLower(path.Base(p)), "changelog") {
		return strings.Join(added, "\n")
	}
	if !codeCommentExt[strings.ToLower(path.Ext(p))] {
		return ""
	}
	var b strings.Builder
	for _, l := range added {
		if strings.HasPrefix(strings.TrimSpace(l), "#!") {
			continue
		}
		if m := commentLineRe.FindStringSubmatch(l); m != nil {
			b.WriteString(strings.TrimSpace(m[1]))
			b.WriteByte('\n')
		}
	}
	return b.String()
}
