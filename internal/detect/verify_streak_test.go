package detect

import (
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/policy"
)

func findRule(sigs []Signal, rule string) *Signal {
	for i := range sigs {
		if sigs[i].Rule == rule {
			return &sigs[i]
		}
	}
	return nil
}

// distinct passing checks with nothing in between flag at the configured run
func TestVerifyStreakFlagsDistinctPassingRunsWithoutProgress(t *testing.T) {
	b := newB(t, "기능 구현 후 검사 실행", nil)
	for i := 0; i < 2; i++ {
		if v := findRule(b.verify("go test ./... -run case"+string(rune('a'+i)), 0, nil), "s1.verify_streak"); v != nil {
			t.Fatalf("must not flag before the configured run count: %+v", v)
		}
	}
	v := findRule(b.verify("go vet ./...", 0, nil), "s1.verify_streak")
	if v == nil {
		t.Fatal("third distinct passing check must flag")
	}
	if !v.Estimate || v.Level != L1 || v.WasteMicro != 0 {
		t.Fatalf("%+v", v)
	}
	if !policy.AdviceOnly(v.Rule) {
		t.Fatalf("estimate rule must be advice only: %+v", v)
	}
	if got := len(v.Evidence); got != 3 {
		t.Fatalf("evidence %d, want 3 runs", got)
	}
}

// a repeat of the same command is s1.identical_rerun, not the streak
func TestVerifyStreakExcludesSameCommandRepeats(t *testing.T) {
	b := newB(t, "기능 구현", nil)
	for i := 0; i < 2; i++ {
		sigs := b.verify("go test ./...", 0, nil)
		if findRule(sigs, "s1.verify_streak") != nil {
			t.Fatalf("same command must not enter the streak")
		}
	}
	if r := findRule(b.verify("go test ./...", 0, nil), "s1.identical_rerun"); r == nil {
		t.Fatal("identical rerun rule must still cover repeats")
	}
}

// failing runs and writes with a following passing check reset the run
func TestVerifyStreakResetsOnFailureAndWriteDrivenProgress(t *testing.T) {
	b := newB(t, "기능 구현", nil)
	for i := 0; i < 3; i++ {
		if findRule(b.verify("go test ./... -run case"+string(rune('a'+i)), 1, nil), "s1.verify_streak") != nil {
			t.Fatal("failing check must reset, not count")
		}
	}
	b2 := newB(t, "기능 구현", nil)
	b2.verify("go test ./...", 0, nil)
	b2.write("x.go", "h1", []string{"+"}, []string{"-"})
	b2.verify("go vet ./...", 0, nil)
	if v := findRule(b2.verify("gofmt -l .", 0, nil), "s1.verify_streak"); v != nil {
		t.Fatalf("first passing check after a write cannot join a run: %+v", v)
	}
	if n := len(b2.e.St.verifyStreak); n != 1 {
		t.Fatalf("run must restart fresh after write driven pass, got %d entries", n)
	}
}
