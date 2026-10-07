package detect

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func scannedUsage(s *State) [4]int64 {
	var total [4]int64
	for _, ev := range s.Events {
		total[0] += ev.Usage.Total()
		if !ev.Priced {
			total[1] += ev.Usage.Total()
		}
		if _, ok := s.Wasted[ev.Seq]; ok {
			total[2] += ev.CostMicroKRW
		}
		if ev.Seq > s.LastProgress {
			total[3] += ev.Usage.Total()
		}
	}
	return total
}

func assertUsage(t *testing.T, s *State) {
	t.Helper()
	a, b, c, d := s.UsageSummary()
	if got, want := [4]int64{a, b, c, d}, scannedUsage(s); got != want {
		t.Fatalf("incremental=%v scan=%v", got, want)
	}
}

func TestUsageSummaryTracksLateZeroCostAndWaste(t *testing.T) {
	e := NewEngine(config.Default(), nil, false, "live", "", "s", "")
	rng := rand.New(rand.NewSource(17))
	for i := 1; i <= 500; i++ {
		ev := &event.Event{Seq: int64(i), Kind: event.KindMessage, Usage: event.Usage{In: int64(i)}, Priced: true, CostMicroKRW: int64(i * 3)}
		e.Observe(ev)
		assertUsage(t, e.St)
		old := e.St.Events[rng.Intn(len(e.St.Events))]
		old.Usage.Out += 7
		old.Priced = !old.Priced
		e.AddCost(old, 0) // tokens change even when no price can be assigned
		assertUsage(t, e.St)
		e.markWaste([]int64{old.Seq}, "repeat")
		assertUsage(t, e.St)
		old.CostMicroKRW += 5
		e.AddCost(old, 5)
		assertUsage(t, e.St)
		if i%13 == 0 {
			e.St.LastProgress = int64(rng.Intn(i))
			assertUsage(t, e.St)
		}
		assertUsage(t, e.St) // unchanged read cannot accumulate twice
	}
}

func TestUsageSummaryIgnoresUnobservedLateUsageUntilAdmission(t *testing.T) {
	e := NewEngine(config.Default(), nil, false, "live", "", "s", "")
	assertUsage(t, e.St)
	ev := &event.Event{Seq: 1, Usage: event.Usage{In: 20}}
	e.AddCost(ev, 0)
	assertUsage(t, e.St)
	e.ObserveWatch(ev)
	assertUsage(t, e.St)
	if total, _, _, _ := e.St.UsageSummary(); total != 20 {
		t.Fatal(total)
	}
}

func BenchmarkUsageSummaryUnchanged(b *testing.B) {
	for _, n := range []int{100, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			s := &State{Wasted: map[int64]string{}}
			for i := 0; i < n; i++ {
				s.Events = append(s.Events, &event.Event{Seq: int64(i + 1), Usage: event.Usage{In: 1}})
			}
			s.UsageSummary()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.UsageSummary()
			}
		})
	}
}
