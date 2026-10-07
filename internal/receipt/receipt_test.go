package receipt

import (
	"strings"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
)

func totals() analyze.Totals {
	return analyze.Totals{
		Micro: 12_480_400_000, Tokens: 1000,
		BucketMicro:   map[string]int64{"progress": 6_910_300_000, "explore": 1_620_400_000, "waste": 3_770_300_000, "other": 179_400_000},
		BucketTokens:  map[string]int64{"progress": 500, "explore": 100, "waste": 300, "other": 100},
		SymptomMicro:  map[string]int64{"S1": 1_840_100_000, "S2": 1_290_100_000, "S3": 640_100_000},
		SymptomTokens: map[string]int64{"S1": 150, "S2": 100, "S3": 50},
		SymptomCount:  map[string]int{}, Interventions: map[string]int{"L1": 3, "L2": 1}, Minutes: 112,
	}
}

func TestLinesSumToTotal(t *testing.T) {
	r := Build(Input{Title: "t", Totals: totals(), Grade: analyze.GradeVerified, Verdicts: []detect.Signal{
		{Detector: "S1", Rule: "s1.identical_rerun", WasteMicro: 1, Facts: map[string]any{"count": 7, "cmd": "npm test"}}}})
	var top, waste, children int64
	for _, l := range r.Lines {
		if l.Indent == 1 {
			top += l.Won
		}
		if l.Key == "waste" {
			waste = l.Won
		}
		if l.Indent == 2 {
			children += l.Won
		}
	}
	if top != r.Total.Won || r.Total.Won != 12480 {
		t.Errorf("top lines %d != total %d", top, r.Total.Won)
	}
	if children != waste {
		t.Errorf("symptom lines %d != waste %d", children, waste)
	}
	txt := r.Text()
	for _, want := range []string{"총 API 환산액", "12,480원", "진척 (검증)", "검증 쳇바퀴", "같은 검증 7회", "1시간 52분", "개입 후보 4회"} {
		if !strings.Contains(txt, want) {
			t.Errorf("text receipt lacks %q:\n%s", want, txt)
		}
	}
}

func TestTokenUnitWhenCoverageLow(t *testing.T) {
	tt := totals()
	tt.UnpricedTokens = 500
	r := Build(Input{Title: "t", Totals: tt, Grade: analyze.GradeEstimated})
	if r.Unit != "tokens" || !strings.Contains(r.Text(), "토큰") || strings.Contains(r.Text(), "12,480원") {
		t.Errorf("low price coverage must switch to tokens:\n%s", r.Text())
	}
}

func TestShareDropsNotes(t *testing.T) {
	r := Build(Input{Title: "t", Totals: totals(), Verdicts: []detect.Signal{{Detector: "S3", Rule: "s3.out_of_scope", WasteMicro: 1, Facts: map[string]any{"path": "src/secret/plan.ts"}}}})
	r.WithEvidence([]detect.Signal{{Detector: "S3", Rule: "s3.out_of_scope", Evidence: []int64{1}}}, map[int64]string{1: "write src/secret/plan.ts"})
	s := r.Share()
	if strings.Contains(s.Text(), "secret") || s.Evidence != nil {
		t.Errorf("share receipt leaks paths:\n%s", s.Text())
	}
}

func TestWidth(t *testing.T) {
	if Width("헛짓") != 4 || Width("abc") != 3 || Width("12,480원") != 8 {
		t.Error("display width")
	}
	if pad("진척", 6) != "진척  " {
		t.Error("pad uses display width")
	}
}
