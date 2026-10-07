package analyze

import (
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

func TestGapsFindUncountedRepeats(t *testing.T) {
	exit := 1
	var seq int64
	ev := func(cat event.Category, tool, bucket, ws string, paths ...string) *event.Event {
		seq++
		e := &event.Event{Seq: seq, Kind: event.KindTool, Category: cat, Tool: tool, Bucket: bucket, WSBefore: ws, Paths: paths, Usage: event.Usage{In: 10}}
		if cat == event.CatVerify {
			e.ExitCode = &exit
		}
		return e
	}
	s := &event.Session{ID: "s1", Agent: "claude", FirstPrompt: "고쳐 줘"}
	s.Events = []*event.Event{
		ev(event.CatVerify, event.ToolShell, BucketOther, "w1"),
		ev(event.CatProduce, event.ToolEdit, BucketOther, "w1", "a.py"),
		ev(event.CatVerify, event.ToolShell, BucketOther, "w2"),
		ev(event.CatVerify, event.ToolShell, BucketExplore, "w2"),
		ev(event.CatProduce, event.ToolEdit, BucketOther, "w2", "README.md"),
		ev(event.CatVerify, event.ToolShell, BucketOther, "w3"),
		ev(event.CatProduce, event.ToolEdit, BucketProgress, "w3", "b.py"),
		ev(event.CatVerify, event.ToolShell, BucketOther, "w4"),
		ev(event.CatVerify, event.ToolShell, BucketWaste, "w4"),
		ev(event.CatExplore, event.ToolTask, BucketOther, "w5"),
		ev(event.CatExplore, event.ToolTask, BucketOther, "w5"),
		ev(event.CatExplore, event.ToolTask, BucketOther, "w6"),
	}
	got := map[string][]Gap{}
	for _, g := range Gaps(&Result{Session: s}) {
		got[g.Pattern] = append(got[g.Pattern], g)
	}
	if v := got[GapVerifyStreak]; len(v) != 1 || v[0].StartSeq != 1 || v[0].EndSeq != 6 || v[0].Tokens != 60 {
		t.Fatalf("four verifications without progress form one streak: %+v", v)
	}
	if v := got[GapDocsVerify]; len(v) != 1 || v[0].StartSeq != 5 || v[0].EndSeq != 6 {
		t.Fatalf("a verification after a docs-only change: %+v", v)
	}
	if v := got[GapReviewRepeat]; len(v) != 1 || v[0].Events != 2 {
		t.Fatalf("reviews over the same state: %+v", v)
	}
}
