package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompareArmsNeedsAControlAndReportsClearDifferences(t *testing.T) {
	var out bytes.Buffer
	compareArms(&out, map[string][][4]float64{"prescription": {{1, 0, 0, 0}}})
	if !strings.Contains(out.String(), "대조군(none) 세션이 아직 없어") {
		t.Fatal(out.String())
	}
	sessions := func(n int, recurred float64) [][4]float64 {
		rows := make([][4]float64, n)
		for i := range rows {
			if float64(i%10) < recurred*10 {
				rows[i][2] = 1
			}
		}
		return rows
	}
	out.Reset()
	compareArms(&out, map[string][][4]float64{"none": sessions(2000, 0.6), "prescription": sessions(2000, 0.2)})
	var line string
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.Contains(l, "재발") {
			line = l
		}
	}
	if !strings.Contains(line, "낮다") || !strings.Contains(line, "-40.0%p") {
		t.Fatalf("a large drop is reported: %q", line)
	}
	out.Reset()
	compareArms(&out, map[string][][4]float64{"none": sessions(20, 0.6), "prescription": sessions(20, 0.5)})
	if strings.Contains(out.String(), "낮다") || strings.Contains(out.String(), "높다") {
		t.Fatalf("a small sample claims nothing:\n%s", out.String())
	}
}
