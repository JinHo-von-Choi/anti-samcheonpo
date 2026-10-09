package clock

import (
	"testing"
	"time"
)

func TestSinceResolvesShortIntervals(t *testing.T) {
	zero := 0
	for i := 0; i < 50; i++ {
		s := Now()
		x := 0
		for j := 0; j < 200; j++ {
			x += j
		}
		_ = x
		if s.Since() <= 0 {
			zero++
		}
	}
	if zero > 5 {
		t.Fatalf("%d of 50 short intervals read as zero", zero)
	}
}

func TestSinceTracksSleep(t *testing.T) {
	s := Now()
	time.Sleep(30 * time.Millisecond)
	if d := s.Since(); d < 25*time.Millisecond || d > time.Second {
		t.Fatalf("slept 30ms, measured %v", d)
	}
}
