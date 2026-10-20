package live

import (
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

// The daemon must not outgrow the host's watch budget. A daemon that holds
// more watchers than the user may have cannot open a watcher for a new
// session, and it takes the user's other tools down with it.
func TestReclaimWatchersStopsTheLeastRecentlyUsed(t *testing.T) {
	restore := watchBudget
	watchBudget = func() int { return 2 }
	t.Cleanup(func() { watchBudget = restore })

	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	db, err := ledger.Open(home + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prices, err := cost.Load("")
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{sessions: map[string]*Session{}, db: db, prices: prices, versions: map[string]string{}}

	caps := adapter.Profiles["claude"].Caps
	var sessions []*Session
	for i := 0; i < 4; i++ {
		s, err := newSession("s"+string(rune('a'+i)), "claude", root, "", db, prices, caps)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.ws.Close() })
		s.mu.Lock()
		s.lastActive = time.Now().Add(time.Duration(i) * time.Minute)
		s.mu.Unlock()
		d.sessions[s.ID] = s
		sessions = append(sessions, s)
	}
	for _, s := range sessions {
		if !s.ws.Watching() {
			t.Fatalf("session %s does not watch before reclaim", s.ID)
		}
	}

	d.mu.Lock()
	d.reclaimWatchers()
	d.mu.Unlock()

	watching := 0
	for _, s := range sessions {
		if s.ws.Watching() {
			watching++
		}
	}
	if watching > 2 {
		t.Fatalf("%d watchers held with a budget of 2", watching)
	}
	// The most recently used session keeps its watch.
	if !sessions[3].ws.Watching() {
		t.Fatal("the most recently used session lost its watch")
	}
	// A reclaimed watch is a reported degradation, not a silent loss.
	for _, s := range sessions {
		if s.ws.Watching() {
			continue
		}
		if got := s.ws.WatchState(); got != WatchStateDegraded {
			t.Fatalf("reclaimed watch state = %q, want %q", got, WatchStateDegraded)
		}
		if got := s.ws.WatchReason(); got != WatchReasonReclaimed {
			t.Fatalf("reclaimed watch reason = %q, want %q", got, WatchReasonReclaimed)
		}
	}
}

// The host budget must leave room for the rest of the user's tools. A
// daemon that takes the whole limit is the problem this cap exists for.
func TestHostWatchBudgetLeavesHeadroom(t *testing.T) {
	budget := hostWatchBudget()
	if budget <= 0 {
		t.Fatalf("host watch budget = %d", budget)
	}
	if budget > defaultWatchBudget {
		t.Fatalf("host watch budget %d exceeds the ceiling %d", budget, defaultWatchBudget)
	}
}
