package intent

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Readers of a result: what they come to it for decides what it has to hold.
const (
	ReaderGuide       = "guide"
	ReaderStatus      = "status"
	ReaderExplanation = "explanation"
	ReaderScreen      = "screen"
)

// Output is what a request asks the result to give its reader: who reads it
// for what, and how many of each element the request named. It is an
// interpretation of the request and grants nothing.
type Output struct {
	Reader  string   `json:"reader,omitempty"`
	Amounts []Amount `json:"amounts,omitempty"`
	// Keep names what a followup asked to leave as it is ("설명은 그대로").
	Keep []string `json:"keep,omitempty"`
}

// Amount is a number of elements a request named. Zero means the request
// ruled the element out.
type Amount struct {
	Element string `json:"element"`
	Count   int    `json:"count"`
}

var readerRules = []struct {
	reader string
	re     *regexp.Regexp
}{
	{ReaderStatus, regexp.MustCompile(`(?i)어디까지|진행 ?상황|상태(만|를|가|는)? ?(알려|말해|뭐|어때|확인)|끝났(어|나|니)|\bstatus\b`)},
	{ReaderScreen, regexp.MustCompile(`(?i)화면|페이지|랜딩|대시보드|모달|\b(ui|screen|page|landing|dashboard|modal)\b`)},
	{ReaderGuide, regexp.MustCompile(`(?i)사용법|(설치|사용) ?(안내|방법|가이드)|README|가이드|매뉴얼|튜토리얼|\b(how[- ]to|usage|guide|tutorial|manual)\b`)},
	{ReaderExplanation, regexp.MustCompile(`(?i)설명해|설명 ?글|개념|\bexplain\b`)},
}

const (
	// Past this length a request is a specification or pasted material, and
	// the numbers in it describe that material.
	maxOutputRequestRunes = 1000
	maxAnswerRequestRunes = 200

	amountElements = `버튼|링크|탭|메뉴|카드|문단|절|섹션|항목|줄`
	amountNumber   = `\d{1,2}|한|두|세|네|다섯`
	amountEnd      = `(?:$|[^가-힣]|은|는|을|를|이|가|로|만|와|과|도|씩)`
)

var (
	// "버튼 2개", "문단은 세 개", "버튼 하나"
	elementFirstRe = regexp.MustCompile(`(?:^|[^가-힣])(` + amountElements + `)(?:은|는|을|를|이|가)?\s*(?:딱|총|단)?\s*(?:(` + amountNumber + `)\s*(?:개|줄|문단|절|가지)|(하나|둘|셋|넷)` + amountEnd + `)`)
	// "버튼은 저장과 취소 두 개"
	elementTopicRe = regexp.MustCompile(`(?:^|[^가-힣])(` + amountElements + `)(?:은|는)\s+[^.\n]{0,24}?\s(` + amountNumber + `)\s*(?:개|가지)`)
	// "세 줄", "2개의 버튼"
	numberFirstRe = regexp.MustCompile(`(?:^|[^가-힣0-9])(` + amountNumber + `)\s*(?:개의?\s*)?(` + amountElements + `)` + amountEnd)
	// "저장과 취소 버튼"
	namedPairRe = regexp.MustCompile(`([가-힣A-Za-z0-9]{1,12})(?:과|와)\s+[가-힣A-Za-z0-9]{1,12}\s+(버튼|링크|탭)` + amountEnd)
	// "버튼 추가 없이", "링크는 넣지 마"
	ruledOutRe = regexp.MustCompile(`(?:^|[^가-힣])(버튼|링크|탭|메뉴|카드)(?:은|는|을|를)?\s*(?:추가\s*|더\s*)?(?:없이|(?:넣|추가하|만들)지\s*(?:마|말)|금지)`)

	keepRe = regexp.MustCompile(`(?:^|[^가-힣A-Za-z0-9])([가-힣A-Za-z0-9]{2,12})(?:은|는)\s*그대로`)

	elementNameRe = regexp.MustCompile(amountElements)
	// "세 줄로 요약해", "한 줄만 답해"
	answerLengthRe = regexp.MustCompile(`(?:^|[^가-힣0-9])(` + amountNumber + `)\s*줄\s*(?:로|만|이내로?|안에)?\s*(?:요약|답|정리|설명|말해|알려|써|적어)`)
)

var numberWords = map[string]int{"한": 1, "하나": 1, "두": 2, "둘": 2, "세": 3, "셋": 3, "네": 4, "넷": 4, "다섯": 5}

func parseAmount(s string) int {
	if n, ok := numberWords[s]; ok {
		return n
	}
	n, _ := strconv.Atoi(s)
	return n
}

// InferOutput reads the reader and the amounts from a request. Requests are
// read oldest first and a later mention replaces an earlier one, so a
// followup that says nothing about an element keeps what was said before.
func InferOutput(goal string) Output {
	var o Output
	if utf8.RuneCountInString(goal) > maxOutputRequestRunes {
		return o
	}
	readerAt := -1
	for _, r := range readerRules {
		if m := r.re.FindAllStringIndex(goal, -1); len(m) > 0 && m[len(m)-1][0] > readerAt {
			readerAt, o.Reader = m[len(m)-1][0], r.reader
		}
	}
	type mention struct {
		at    int
		count int
	}
	found := map[string]mention{}
	note := func(element string, at, count int) {
		if prev, ok := found[element]; !ok || at >= prev.at {
			found[element] = mention{at, count}
		}
	}
	group := func(s string, m []int, i int) string {
		if m[2*i] < 0 {
			return ""
		}
		return s[m[2*i]:m[2*i+1]]
	}
	for _, m := range elementFirstRe.FindAllStringSubmatchIndex(goal, -1) {
		n := group(goal, m, 2)
		if n == "" {
			n = group(goal, m, 3)
		}
		note(group(goal, m, 1), m[0], parseAmount(n))
	}
	for _, m := range elementTopicRe.FindAllStringSubmatchIndex(goal, -1) {
		note(group(goal, m, 1), m[0], parseAmount(group(goal, m, 2)))
	}
	for _, m := range numberFirstRe.FindAllStringSubmatchIndex(goal, -1) {
		note(group(goal, m, 2), m[0], parseAmount(group(goal, m, 1)))
	}
	for _, m := range namedPairRe.FindAllStringSubmatchIndex(goal, -1) {
		// "공유 버튼과 도움말 링크" names two different elements
		if !elementNameRe.MatchString(group(goal, m, 1)) {
			note(group(goal, m, 2), m[0], 2)
		}
	}
	for _, m := range ruledOutRe.FindAllStringSubmatchIndex(goal, -1) {
		note(group(goal, m, 1), m[0], 0)
	}
	for _, m := range keepRe.FindAllStringSubmatch(goal, -1) {
		if !slices.Contains(o.Keep, m[1]) {
			o.Keep = append(o.Keep, m[1])
		}
	}
	for element, m := range found {
		o.Amounts = append(o.Amounts, Amount{Element: element, Count: m.count})
	}
	sort.Slice(o.Amounts, func(i, j int) bool {
		return found[o.Amounts[i].Element].at < found[o.Amounts[j].Element].at
	})
	return o
}

// Count reports how many of an element the request named.
func (o Output) Count(element string) (int, bool) {
	for _, a := range o.Amounts {
		if a.Element == element {
			return a.Count, true
		}
	}
	return 0, false
}

// AnswerLines reports the length of the answer a short request set ("세 줄로
// 요약해"). A count of lines of code ("3줄 고쳐") is not one.
func AnswerLines(request string) (int, bool) {
	if utf8.RuneCountInString(request) > maxAnswerRequestRunes {
		return 0, false
	}
	m := answerLengthRe.FindStringSubmatch(request)
	if m == nil {
		return 0, false
	}
	n := parseAmount(m[1])
	return n, n > 0
}

// ReaderText says what the reader needs from the result.
func (o Output) ReaderText() string {
	return map[string]string{
		ReaderGuide:       "사용 안내 - 무엇을 할 수 있고 어떻게 실행하는가",
		ReaderStatus:      "상태 답변 - 무엇이 끝났고 지금 판단할 일은 무엇인가",
		ReaderExplanation: "설명 글 - 요청한 개념을 이해하는 데 필요한 연결",
		ReaderScreen:      "화면 - 사용자가 여기서 마치는 일",
	}[o.Reader]
}

// AmountText lists the amounts for the goal card.
func (o Output) AmountText() string {
	parts := make([]string, 0, len(o.Amounts))
	for _, a := range o.Amounts {
		switch {
		case a.Count == 0:
			parts = append(parts, a.Element+" 추가 없음")
		case a.Element == "줄":
			parts = append(parts, fmt.Sprintf("%d줄", a.Count))
		default:
			parts = append(parts, fmt.Sprintf("%s %d개", a.Element, a.Count))
		}
	}
	return strings.Join(parts, ", ")
}
