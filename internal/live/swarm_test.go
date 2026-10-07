package live

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"testing"
)

func TestSwarmGovernorBlocksWholeLineageWhenTreeBudgetExceeded(t *testing.T) {
	g := NewSwarmGovernor(1000)
	root := g.RegisterSession("root", "")
	child := g.RegisterSession("child", "root")
	grand := g.RegisterSession("grand", "child")
	sibling := g.RegisterSession("sibling", "root")

	if root.ParentID() != "" || child.ParentID() != "root" || grand.ParentID() != "child" || sibling.ParentID() != "root" {
		t.Fatalf("lineage not registered: root=%q child=%q grand=%q sibling=%q", root.ParentID(), child.ParentID(), grand.ParentID(), sibling.ParentID())
	}

	g.RecordSpend("root", 400)
	if g.IsSessionBlocked("grand") || g.IsSessionBlocked("child") || g.IsSessionBlocked("root") || g.IsSessionBlocked("sibling") {
		t.Fatal("lineage blocked before the tree budget was exceeded")
	}
	if g.TreeTotalSpend("root") != 400 {
		t.Fatalf("tree total = %d, want 400", g.TreeTotalSpend("root"))
	}

	g.RecordSpend("grand", 700)
	if g.TreeTotalSpend("root") != 1100 {
		t.Fatalf("tree total = %d, want 1100", g.TreeTotalSpend("root"))
	}

	for _, id := range []string{"root", "child", "grand", "sibling"} {
		if !g.IsSessionBlocked(id) {
			t.Fatalf("session %q survived the tree budget overrun", id)
		}
	}
}

func TestSwarmGovernorTreeSpendAccumulatesOnSessionAndAncestors(t *testing.T) {
	g := NewSwarmGovernor(1_000_000)
	root := g.RegisterSession("root", "")
	child := g.RegisterSession("child", "root")
	grand := g.RegisterSession("grand", "child")

	g.RecordSpend("grand", 10)
	g.RecordSpend("child", 20)
	g.RecordSpend("root", 5)

	if grand.SpendMicroKRW() != 10 || child.SpendMicroKRW() != 20 || root.SpendMicroKRW() != 5 {
		t.Fatalf("session spend mixed with lineage spend: root=%d child=%d grand=%d", root.SpendMicroKRW(), child.SpendMicroKRW(), grand.SpendMicroKRW())
	}
	if root.TreeMicroKRW() != 35 || child.TreeMicroKRW() != 30 || grand.TreeMicroKRW() != 10 {
		t.Fatalf("tree spend = root %d child %d grand %d, want 35/30/10", root.TreeMicroKRW(), child.TreeMicroKRW(), grand.TreeMicroKRW())
	}
	if g.TreeTotalSpend("child") != 35 || g.TreeTotalSpend("grand") != 35 || g.TreeTotalSpend("root") != 35 {
		t.Fatalf("tree totals diverged: child=%d grand=%d root=%d", g.TreeTotalSpend("child"), g.TreeTotalSpend("grand"), g.TreeTotalSpend("root"))
	}
	if got := g.Children("grand"); len(got) != 0 {
		t.Fatalf("leaf reported children: %v", got)
	}
	if got := g.Children("root"); fmt.Sprint(got) != "[child grand]" {
		t.Fatalf("children = %v, want [child grand]", got)
	}
	if got := g.Children("missing"); len(got) != 0 {
		t.Fatalf("unknown session reported children: %v", got)
	}
}

func TestSwarmGovernorKeepsIndependentTreesIsolated(t *testing.T) {
	g := NewSwarmGovernor(500)
	g.RegisterSession("a-root", "")
	g.RegisterSession("a-child", "a-root")
	g.RegisterSession("b-root", "")
	g.RegisterSession("b-child", "b-root")

	g.RecordSpend("a-child", 400)
	if g.IsSessionBlocked("a-child") || g.IsSessionBlocked("b-child") {
		t.Fatal("an independent tree was blocked by another tree's spend")
	}

	g.RecordSpend("a-root", 200)
	for _, id := range []string{"a-root", "a-child"} {
		if !g.IsSessionBlocked(id) {
			t.Fatalf("overrunning tree session %q was not blocked", id)
		}
	}
	for _, id := range []string{"b-root", "b-child"} {
		if g.IsSessionBlocked(id) {
			t.Fatalf("independent tree session %q was blocked", id)
		}
	}
	if g.TreeTotalSpend("b-root") != 0 || g.TreeTotalSpend("b-child") != 0 {
		t.Fatalf("independent tree total changed: root=%d child=%d", g.TreeTotalSpend("b-root"), g.TreeTotalSpend("b-child"))
	}
}

func TestSwarmGovernorRegisterAndSpendOfUnknownSessionsStayAccounted(t *testing.T) {
	g := NewSwarmGovernor(100)
	// A parent that has not registered yet still gets an explicit lineage node,
	// so no spend is attributed to a fabricated tree.
	orphan := g.RegisterSession("child", "late-parent")
	if orphan.ParentID() != "late-parent" {
		t.Fatalf("parent link dropped: %q", orphan.ParentID())
	}
	if got := g.Children("late-parent"); fmt.Sprint(got) != "[child]" {
		t.Fatalf("children = %v, want [child]", got)
	}

	g.RecordSpend("child", 60)
	g.RecordSpend("stranger", 60)
	if g.TreeTotalSpend("stranger") != 60 {
		t.Fatalf("spend of an unknown session was dropped: %d", g.TreeTotalSpend("stranger"))
	}
	if got := g.Children("stranger"); len(got) != 0 {
		t.Fatalf("stranger adopted children: %v", got)
	}
	if g.IsSessionBlocked("stranger") {
		t.Fatal("two separate trees blocked each other")
	}

	g.RecordSpend("child", 60)
	if !g.IsSessionBlocked("child") || !g.IsSessionBlocked("late-parent") {
		t.Fatal("tree budget overrun did not block the session and its implicit parent")
	}
	if g.IsSessionBlocked("stranger") {
		t.Fatal("two separate trees blocked each other")
	}
}

func TestSwarmGovernorRejectsCyclesAndIgnoresNegativeSpend(t *testing.T) {
	g := NewSwarmGovernor(1_000_000)
	a := g.RegisterSession("a", "b")
	b := g.RegisterSession("b", "a")
	if a.ParentID() != "b" {
		t.Fatalf("first lineage edge dropped: %q", a.ParentID())
	}
	if b.ParentID() != "" {
		t.Fatalf("cycle accepted as lineage: %q", b.ParentID())
	}
	self := g.RegisterSession("self", "self")
	if self.ParentID() != "" {
		t.Fatalf("self parent accepted: %q", self.ParentID())
	}

	g.RecordSpend("a", -500)
	g.RecordSpend("a", 100)
	if a.SpendMicroKRW() != 100 || g.TreeTotalSpend("a") != 100 {
		t.Fatalf("negative spend was accumulated: spend=%d tree=%d", a.SpendMicroKRW(), g.TreeTotalSpend("a"))
	}
	if got := g.Children("b"); fmt.Sprint(got) != "[a]" {
		t.Fatalf("children = %v, want [a]", got)
	}
}

func TestSwarmGovernorTreeSpendSaturatesInsteadOfWrapping(t *testing.T) {
	g := NewSwarmGovernor(10)
	g.RegisterSession("root", "")
	g.RecordSpend("root", math.MaxInt64)
	g.RecordSpend("root", math.MaxInt64)
	if g.TreeTotalSpend("root") != math.MaxInt64 {
		t.Fatalf("tree spend overflowed: %d", g.TreeTotalSpend("root"))
	}
	if !g.IsSessionBlocked("root") {
		t.Fatal("overflowed tree was not blocked")
	}
}

func TestSwarmGovernorConcurrentLineageAccessIsSafe(t *testing.T) {
	const workers, rounds, unit = 8, 40, 3
	g := NewSwarmGovernor(math.MaxInt64)
	root := g.RegisterSession("root", "")
	if root.ID() != "root" {
		t.Fatalf("root id = %q", root.ID())
	}

	var wg sync.WaitGroup
	for w := range workers {
		id := fmt.Sprintf("worker-%d", w)
		node := g.RegisterSession(id, "root")
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range rounds {
				g.RecordSpend(id, int64(unit*(r+1)))
				_ = g.TreeTotalSpend("root")
				_ = g.TreeTotalSpend(id)
				_ = g.Children("root")
				_ = g.IsSessionBlocked(id)
				_ = node.SpendMicroKRW()
				_ = node.TreeMicroKRW()
				_ = node.Blocked()
				_ = node.ParentID()
				_ = g.RegisterSession(fmt.Sprintf("%s-tmp-%d", id, r), id).ID()
			}
		}()
	}
	wg.Wait()

	var want int64
	for range workers {
		for r := range rounds {
			want += int64(unit * (r + 1))
		}
	}
	if got := g.TreeTotalSpend("root"); got != want {
		t.Fatalf("concurrent tree spend = %d, want %d", got, want)
	}
	children := g.Children("root")
	sorted := append([]string(nil), children...)
	sort.Strings(sorted)
	if len(sorted) != workers+workers*rounds {
		t.Fatalf("children = %d, want %d", len(sorted), workers+workers*rounds)
	}
	for i, c := range sorted {
		if c == "root" {
			t.Fatal("a session listed as its own child")
		}
		if i > 0 && sorted[i-1] == c {
			t.Fatalf("duplicate child %q", c)
		}
	}
}
