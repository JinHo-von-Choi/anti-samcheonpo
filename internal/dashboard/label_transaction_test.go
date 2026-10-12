package dashboard

import (
	"net/http"
	"sync"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
)

func TestDashboardSharesLabelLockAndVotesAcrossServers(t *testing.T) {
	s, _ := dashboardFixture(t)
	other, err := New(s.Options)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"session":"fixture-session","labeler":"human-a","start":0,"end":2,"symptom":"S4","justified":"yes","outcome":"unknown"}`
	unlock, err := eval.LockLabels(s.LabelsDir)
	if err != nil {
		t.Fatal(err)
	}
	blocked := perform(s, "POST", "/api/label", body, true)
	unlock()
	if blocked.Code != http.StatusConflict {
		t.Fatalf("web ignored common lock: %d %s", blocked.Code, blocked.Body)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, server := range []*Server{s, other} {
		wg.Add(1)
		go func(server *Server) {
			defer wg.Done()
			codes <- perform(server, "POST", "/api/label", body, true).Code
		}(server)
	}
	wg.Wait()
	close(codes)
	success := 0
	for code := range codes {
		if code == http.StatusOK {
			success++
		}
	}
	labels, err := eval.LoadLabels(s.LabelsDir)
	if success != 1 || err != nil || len(labels) != 1 {
		t.Fatalf("simultaneous duplicate submission: successes=%d labels=%v err=%v", success, labels, err)
	}
	if got := perform(other, "POST", "/api/label", body, true); got.Code == http.StatusOK {
		t.Fatal("second server accepted a duplicate answer")
	}
}
