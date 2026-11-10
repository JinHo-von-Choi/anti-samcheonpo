package detect

import (
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kinds of a sentence a reader can lose without losing anything.
const (
	// FillerRestate opens a document by saying back what was asked for.
	FillerRestate = "restated_request"
	// FillerRepeat says again what the same write already said.
	FillerRepeat = "repeated_explanation"
	// FillerLimit disclaims what the work did not prove, which tells a reader
	// of the document nothing they can act on.
	FillerLimit = "unneeded_limit"
	// FillerConditions lists circumstances that might change a result without
	// saying which one applies or what to do about it.
	FillerConditions = "condition_list"
)

const (
	// A long request is a specification, and a document built from one
	// shares its words on purpose.
	fillerMaxRequestRunes = 300
	fillerRestateShare    = 0.4
	fillerRestateMinPairs = 6
	fillerRepeatSimilar   = 0.8
	fillerRepeatMinPairs  = 16
	fillerMaxSentences    = 100
)

// docFrameRe is a sentence that introduces the document instead of saying
// something: "이 문서는 ~를 설명한다".
var docFrameRe = regexp.MustCompile(`^(이|본)\s?(문서|글|페이지|가이드|절|장)(는|은|에서는|에서)\s|^(아래|다음)(는|은|에서는)\s|(?i)^this (document|guide|page|section|article)\s`)

// disclaimerRe says that something was not proven or measured. A limit on what
// a feature does ("보존을 보장하지 않는다") is a condition the reader needs and
// is not matched.
var disclaimerRe = regexp.MustCompile(`(입증|검증|측정|평가)(한|된)\s*(것은|것이)\s*아니|입증(하지|되지)\s*(않|못)|(?i)\b(has|have) not been (proven|verified|measured|evaluated)\b|\bdoes not (prove|establish|demonstrate)\b|\bis not (proven|verified)\b`)

var (
	conditionMarkRe  = regexp.MustCompile(`(경우|때|상황|환경|조건|여부|규모|버전|설정|종류)(에|에는|에서|마다|와|과|나|이나|및|,|에 따라)|(?i)\bdepending on\b`)
	conditionHedgeRe = regexp.MustCompile(`(다를|달라질|바뀔|변할|영향을 받을|차이가 날|차이가 있을)\s*수\s*있|(?i)\b(may|can|might) (vary|differ|change)\b`)
	englishListRe    = regexp.MustCompile(`(?i)\bdepending on\b[^.]*,[^.]*\b(and|or)\b`)
	digitRe          = regexp.MustCompile(`\d`)
)

// conditionList reports a sentence that names several circumstances and only
// concludes that the result may differ. One with a number or a command
// states a condition the reader can act on.
const minListedConditions = 3

func conditionList(s string) bool {
	if digitRe.MatchString(s) || !conditionHedgeRe.MatchString(s) {
		return false
	}
	return englishListRe.MatchString(s) || len(conditionMarkRe.FindAllString(s, -1)) >= minListedConditions
}

// ReaderDocument reports a file written for people to read: a document or a
// README, and not a plan, a note or a change log.
func ReaderDocument(p string) bool {
	base := strings.ToLower(path.Base(p))
	return !metaExempt(p) && !strings.HasPrefix(base, "changelog") && (IsDocPath(p) || strings.HasPrefix(base, "readme"))
}

// fillerSentences finds, in what a write adds to a document, the opening that
// repeats the request and the sentences that repeat each other. The share of
// matching letter pairs is used because Korean endings change the words
// themselves.
func fillerSentences(p string, added []string, request string) []MetaHit {
	if !ReaderDocument(p) {
		return nil
	}
	var ss []string
	for _, s := range sentences(stripCode(strings.Join(added, "\n"))) {
		if !strings.HasPrefix(s, "|") {
			ss = append(ss, s)
		}
	}
	if len(ss) > fillerMaxSentences {
		ss = ss[:fillerMaxSentences]
	}
	var out []MetaHit
	for _, s := range ss {
		if disclaimerRe.MatchString(s) {
			out = append(out, MetaHit{Kind: FillerLimit, Sentence: clip(s, 90), Path: p})
		} else if conditionList(s) {
			out = append(out, MetaHit{Kind: FillerConditions, Sentence: clip(s, 90), Path: p})
		}
	}
	if utf8.RuneCountInString(request) <= fillerMaxRequestRunes {
		asked := letterPairs(request)
		for _, s := range ss {
			if !docFrameRe.MatchString(s) {
				continue
			}
			body := letterPairs(docFrameRe.ReplaceAllString(s, ""))
			if len(body) >= fillerRestateMinPairs && shareIn(body, asked) >= fillerRestateShare {
				out = append(out, MetaHit{Kind: FillerRestate, Sentence: clip(s, 90), Path: p})
			}
		}
	}
	pairs := make([]map[string]bool, len(ss))
	for i, s := range ss {
		pairs[i] = letterPairs(s)
	}
	for i := range ss {
		if len(pairs[i]) < fillerRepeatMinPairs {
			continue
		}
		for j := 0; j < i; j++ {
			if len(pairs[j]) >= fillerRepeatMinPairs && similarity(pairs[i], pairs[j]) >= fillerRepeatSimilar {
				out = append(out, MetaHit{Kind: FillerRepeat, Sentence: clip(ss[i], 90), Path: p})
				break
			}
		}
	}
	return out
}

// letterPairs is the set of adjacent letters and digits of a text.
func letterPairs(s string) map[string]bool {
	var letters []rune
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letters = append(letters, r)
		}
	}
	out := make(map[string]bool, len(letters))
	for i := 1; i < len(letters); i++ {
		out[string(letters[i-1:i+1])] = true
	}
	return out
}

func shareIn(a, b map[string]bool) float64 {
	if len(a) == 0 {
		return 0
	}
	n := 0
	for k := range a {
		if b[k] {
			n++
		}
	}
	return float64(n) / float64(len(a))
}

func similarity(a, b map[string]bool) float64 {
	n := 0
	for k := range a {
		if b[k] {
			n++
		}
	}
	union := len(a) + len(b) - n
	if union == 0 {
		return 0
	}
	return float64(n) / float64(union)
}
