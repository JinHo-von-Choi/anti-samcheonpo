package live

import "testing"

// Lineage from a handoff link or a declared parent forms one tree; the tree
// gets a governor only when it has a budget, a session whose usage already
// includes its descendants is not charged twice, and a session with no
// lineage is never bounded.
func TestSwarmRegistryTreesAndDoubleCounting(t *testing.T) {
	var r swarmRegistry
	r.register("child", "root", false, 0)
	if _, _, _, bounded := r.state("child"); bounded {
		t.Fatal("a tree without a budget is lineage only")
	}
	r.register("grandchild", "child", true, 3)
	spent, limit, blocked, bounded := r.state("root")
	if !bounded || limit != 3 || spent != 0 || blocked {
		t.Fatalf("the budget bounds the whole tree from its root: %d %d %v %v", spent, limit, blocked, bounded)
	}
	r.record("grandchild", 2_000_000)
	r.record("great", 5_000_000) // not registered: its own tree, untouched
	if spent, _, blocked, _ := r.state("root"); spent != 2 || blocked {
		t.Fatalf("spend climbs to the root: %d %v", spent, blocked)
	}
	r.register("leaf", "grandchild", false, 0)
	r.record("leaf", 9_000_000) // grandchild declared it includes its descendants
	if spent, _, blocked, _ := r.state("child"); spent != 2 || blocked {
		t.Fatalf("a descendant of an including session is not charged again: %d %v", spent, blocked)
	}
	r.record("child", 2_000_000)
	if _, _, blocked, _ := r.state("leaf"); !blocked {
		t.Fatal("past the budget every session of the tree is blocked")
	}
	if _, _, blocked, bounded := r.state("great"); blocked || bounded {
		t.Fatal("another tree is untouched")
	}
	r.register("root", "leaf", false, 0) // a cycle attempt keeps the existing lineage
	if r.rootLocked("leaf") != "root" {
		t.Fatal("lineage is a fact: the first registration stands")
	}
}
