package live

import (
	"sync"
	"testing"
	"time"
)

func TestSharedResourceReservations(t *testing.T) {
	r := &resourceRegistry{}
	now := time.Now()
	read := []ResourceClaim{{"database", "db-a", "read"}}
	write := []ResourceClaim{{"database", "db-a", "write"}}
	if r.reserve("a", "1", read, now) != "" || r.reserve("b", "2", read, now) != "" {
		t.Fatal("independent reads conflicted")
	}
	if r.reserve("c", "3", write, now) == "" {
		t.Fatal("write did not conflict")
	}
	r.release("a", "")
	r.release("b", "")
	r.release("c", "")
	var wg sync.WaitGroup
	var mu sync.Mutex
	conflicts := 0
	for _, id := range []string{"d", "e"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if r.reserve(id, "1", write, now) != "" {
				mu.Lock()
				conflicts++
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	if conflicts != 1 {
		t.Fatalf("atomic reservations: %d conflicts", conflicts)
	}
	if r.reserve("f", "1", write, now.Add(31*time.Minute)) != "" {
		t.Fatal("missing result held resource forever")
	}
}
