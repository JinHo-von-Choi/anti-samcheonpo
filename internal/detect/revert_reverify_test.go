package detect

import "testing"

func TestRevertReverifyLoops(t *testing.T) {
	b := newB(t, "버그 고쳐 줘", nil)
	loop := func() *Signal {
		before := b.ws
		b.write("src/a.rs", "fix", []string{"a"}, []string{"b"})
		fixed := b.ws
		b.verify("cargo test --test a_test", 0, nil)
		b.ws = before // the fix undone (git stash)
		b.verify("cargo test --test a_test", 101, []string{"a_test::case"})
		b.ws = fixed // restored (git stash pop)
		return fired(b.verify("cargo test --test a_test", 0, nil), "s1.revert_reverify")
	}
	if v := loop(); v == nil || v.Level != L0 || v.Facts["count"] != 1 {
		t.Fatalf("the first loop is recorded: %+v", v)
	}
	if v := loop(); v == nil || v.Level != L1 || v.Facts["count"] != 2 {
		t.Fatalf("the second loop is advised: %+v", v)
	}
}

func TestRevertReverifyLeavesOrdinaryWorkAlone(t *testing.T) {
	b := newB(t, "x", nil)
	b.write("src/a.rs", "h1", []string{"a"}, nil)
	b.verify("cargo test", 1, []string{"t::a"})
	b.write("src/a.rs", "h2", []string{"b"}, nil)
	b.verify("cargo test", 0, nil)
	b.write("src/b.rs", "h3", []string{"c"}, nil)
	if v := fired(b.verify("cargo test", 0, nil), "s1.revert_reverify"); v != nil {
		t.Fatalf("forward progress is no loop: %+v", v)
	}
	asked := newB(t, "수정 전에 시험이 실패하는지 먼저 확인하고 고쳐 줘 (red-green)", nil)
	before := asked.ws
	asked.write("src/a.rs", "fix", []string{"a"}, nil)
	fixed := asked.ws
	asked.verify("cargo test", 0, nil)
	asked.ws = before
	asked.verify("cargo test", 1, []string{"t::a"})
	asked.ws = fixed
	if v := fired(asked.verify("cargo test", 0, nil), "s1.revert_reverify"); v != nil {
		t.Fatalf("a requested red-green check: %+v", v)
	}
}
