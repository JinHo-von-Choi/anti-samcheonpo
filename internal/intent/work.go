package intent

import (
	"regexp"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// Work describes an interpretation of requested work, never execution authority.
type Work struct {
	Kind      string   `json:"kind"`
	Phase     string   `json:"phase"`
	Remaining []string `json:"remaining"`
	Basis     string   `json:"basis"`
}

var researchWords = regexp.MustCompile(`(?i)(검토|리뷰|분석|조사|파악|설명|웹서치|비교|진단|research|review|analy[sz]|investigate|explain|summari[sz])`)
var documentWords = regexp.MustCompile(`(?i)(플랜|계획|보고서|문서|리포트|설계안|report|document|plan|proposal)`)
var implementationWords = regexp.MustCompile(`(?i)(구현|코딩|코드.*(?:고쳐|수정)|버그.*(?:고쳐|수정)|리팩터|implement|refactor|fix.*(?:bug|code))`)

func InferWork(goal string) Work {
	w := Work{Kind: "implementation", Phase: "discovery", Basis: "inferred_user_request", Remaining: []string{"변경과 검증 근거 확인"}}
	if implementationWords.MatchString(goal) {
		return w
	}
	if researchWords.MatchString(goal) {
		w.Kind, w.Phase = "research", "research"
		w.Remaining = []string{"미해결 질문과 근거 확인"}
		if documentWords.MatchString(goal) {
			w.Kind = "research_document"
			w.Remaining = append(w.Remaining, "결과 문서 작성")
		}
	} else if documentWords.MatchString(goal) {
		w.Kind, w.Phase = "document", "synthesis"
		w.Remaining = []string{"요청한 문서와 근거 확인"}
	}
	return w
}

func DescribeWork(goal string, events []*event.Event) Work {
	w := InferWork(goal)
	for _, ev := range events {
		if ev.Kind != event.KindTool || ev.IsError && ev.Category != event.CatVerify {
			continue
		}
		if ev.Category == event.CatProduce {
			if w.Kind == "research_document" || w.Kind == "document" {
				w.Phase = "synthesis"
			} else {
				w.Phase = "implementation"
			}
		}
		if ev.Category == event.CatVerify && ev.ExitCode != nil && *ev.ExitCode >= 0 && !ev.Background {
			w.Phase = "verification"
		}
	}
	return w
}

func ResearchRequest(goal string) bool {
	k := InferWork(strings.TrimSpace(goal)).Kind
	return k == "research" || k == "research_document"
}

func (w Work) KindText() string {
	if text := map[string]string{"implementation": "구현", "research": "조사", "research_document": "조사 후 문서 작성", "document": "문서 작성"}[w.Kind]; text != "" {
		return text
	}
	return "미확인"
}
func (w Work) PhaseText() string {
	if text := map[string]string{"discovery": "현황 파악", "research": "근거 조사", "synthesis": "결과 정리", "implementation": "변경 작업", "verification": "검증"}[w.Phase]; text != "" {
		return text
	}
	return "미확인"
}
