package alignment

import "testing"

func TestDeliverablesFromRequests(t *testing.T) {
	cases := []struct {
		msg  string
		want []Deliverable
	}{
		{"v0.13.0 릴리즈해", []Deliverable{{Action: ActRelease, Target: "v0.13.0"}}},
		{"커밋만 해, 푸시는 하지 마", []Deliverable{{Action: ActCommit}, {Action: ActPush, Forbidden: true}}},
		{"테스트 통과하면 릴리즈해", []Deliverable{{Action: ActRelease, Conditional: true}}},
		{"PR해도 되겠어?", nil},
		{"릴리즈 노트 확인해", nil},
		{"`old/cache.db` 지워", []Deliverable{{Action: ActDelete, Target: "old/cache.db"}}},
	}
	for _, c := range cases {
		got := Deliverables(c.msg, 1)
		if len(got) != len(c.want) {
			t.Fatalf("%q: %+v", c.msg, got)
		}
		for i, w := range c.want {
			g := got[i]
			if g.Action != w.Action || g.Target != w.Target || g.Forbidden != w.Forbidden || g.Conditional != w.Conditional {
				t.Fatalf("%q: %+v, want %+v", c.msg, g, w)
			}
		}
	}
}

func TestBookTracksAsksAgainstCommands(t *testing.T) {
	var b Book
	b.Add(Deliverables("v0.13.0 릴리즈해", 1))
	b.Observe(Classify("git tag v0.13.0", nil))
	if o := b.Owed(); len(o) != 1 || o[0].State != StateMissing {
		t.Fatal("a tag met the release")
	}
	// a pushed tag may start a release workflow outside the session
	b.Observe(Classify("git push origin v0.13.0", nil))
	if o := b.Owed(); len(o) != 1 || o[0].State != StateUnobservable {
		t.Fatalf("a pushed tag: %+v", o)
	}
	b.Observe(Classify("gh release create v0.12.0", nil))
	if o := b.Owed(); len(o) != 1 || o[0].Other != "v0.12.0" {
		t.Fatalf("another version met the release: %+v", o)
	}
	b.Observe(Classify("gh release create v0.13.0 --notes x", nil))
	if len(b.Owed()) != 0 {
		t.Fatal("the release was not observed")
	}
	b.Add(Deliverables("배포해", 2))
	b.Observe(Classify("make deploy", nil))
	if o := b.Owed(); len(o) != 1 || o[0].State != StateUnobservable {
		t.Fatalf("a wrapper is not unobservable: %+v", o)
	}
	b.Add(Deliverables("배포는 하지 마", 3))
	if len(b.Owed()) != 0 || b.Forbidden(ActDeploy) == nil {
		t.Fatal("the current request did not win")
	}
	b.Add(Deliverables("이제 배포해", 4))
	if b.Forbidden(ActDeploy) != nil {
		t.Fatal("a later ask did not lift the prohibition")
	}
}
