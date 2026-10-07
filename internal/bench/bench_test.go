package bench

import (
	"path/filepath"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
)

// TestSeedScenarios runs ProgressBench twice: every scenario passes and the
// two runs agree.
func TestSeedScenarios(t *testing.T) {
	sc, err := Load(filepath.Join("..", "..", "bench", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sc) < 24 {
		t.Fatalf("expected the seed set, got %d", len(sc))
	}
	perSymptom := map[string]int{}
	prices, _ := cost.Load("")
	for _, s := range sc {
		a, err := Run(s, prices, t.TempDir())
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		b, err := Run(s, prices, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if !a.Pass {
			t.Errorf("%s: %v (fired %v)", s.Name, a.Problems, a.Fired)
		}
		if a.WasteKRW != b.WasteKRW || len(a.Fired) != len(b.Fired) {
			t.Errorf("%s: runs differ", s.Name)
		}
		if s.Symptom == "normal" && a.MaxLevel >= 2 {
			t.Errorf("%s: normal work got L%d", s.Name, a.MaxLevel)
		}
		perSymptom[s.Symptom]++
	}
	for _, sym := range []string{"S1", "S2", "S3", "S4", "S5", "S7", "S8"} {
		if perSymptom[sym] < 3 {
			t.Errorf("%s has %d scenarios, need 3", sym, perSymptom[sym])
		}
	}
}
