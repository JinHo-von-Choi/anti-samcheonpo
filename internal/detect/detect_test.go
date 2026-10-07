package detect

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

// builder feeds hand-made events into an engine.
type builder struct {
	t   *testing.T
	e   *Engine
	seq int64
	ts  time.Time
	ws  string
	n   int
}

func newB(t *testing.T, prompt string, c *contract.Contract) *builder {
	cfg := config.Default()
	cfg.Detectors.Overrides = map[string]int{}
	return &builder{t: t, e: NewEngine(cfg, c, c != nil, "audit", "/w", "s", prompt), ts: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), ws: "ws0"}
}

func (b *builder) next(ev *event.Event) []Signal {
	ev.Seq = b.seq
	b.seq++
	b.ts = b.ts.Add(20 * time.Second)
	ev.TS = b.ts
	if ev.CostMicroKRW == 0 && ev.Kind == event.KindTool {
		ev.CostMicroKRW = 50_000_000 // 50 won
	}
	ev.WSBefore = b.ws
	if ev.Category == event.CatProduce {
		b.n++
		b.ws = "ws" + string(rune('a'+b.n%26)) + time.Duration(b.n).String()
	}
	ev.WSAfter = b.ws
	return b.e.Observe(ev)
}

func (b *builder) verify(cmd string, exit int, failed []string, errs ...string) []Signal {
	norm, _ := fp.NormalizeCmd(cmd)
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Cmd: cmd, CmdNorm: norm, CmdFP: fp.CmdFP(norm), Category: event.CatVerify,
		ExitCode: &exit, FailedTests: failed, ErrFPs: errs}
	ev.ResultFP = fp.ResultFP(ev.ExitCode, errs, failed)
	return b.next(ev)
}

func (b *builder) write(path, hash string, added, removed []string) []Signal {
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Paths: []string{path},
		WriteHashes: map[string]string{path: hash}, AddedLines: map[string]int{path: len(added)}, RemovedLines: map[string]int{path: len(removed)},
		Patch: []event.PatchFile{{Path: path, Added: added, Removed: removed}}}
	return b.next(ev)
}

func (b *builder) read(path string) []Signal {
	return b.next(&event.Event{Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{path}})
}

func (b *builder) msg(text string) []Signal {
	ev := &event.Event{Kind: event.KindMessage, Text: text}
	b.next(ev)
	return b.e.TurnEnd(ev, nil)
}

func fired(sigs []Signal, rule string) *Signal {
	for i := range sigs {
		if sigs[i].Rule == rule {
			return &sigs[i]
		}
	}
	return nil
}

func all(e *Engine, rule string) []Signal {
	var out []Signal
	for _, v := range e.Verdicts {
		if v.Rule == rule {
			out = append(out, v)
		}
	}
	return out
}

func TestIdenticalRerun(t *testing.T) {
	b := newB(t, "src/a.py 고쳐 줘", nil)
	b.write("src/a.py", "h1", []string{"x"}, nil)
	if s := b.verify("pytest", 1, []string{"t::a"}); fired(s, "s1.identical_rerun") != nil {
		t.Fatal("first run is not a rerun")
	}
	s := b.verify("pytest 2>&1 | tail -20", 1, []string{"t::a"})
	v := fired(s, "s1.identical_rerun")
	if v == nil || v.Level != L0 || v.WasteMicro != 50_000_000 {
		t.Fatalf("second identical run is confirmed waste at L0: %+v", v)
	}
	v = fired(b.verify("pytest", 1, []string{"t::a"}), "s1.identical_rerun")
	if v == nil || v.Level != L1 || v.Confidence != 0.95 || len(v.Evidence) != 3 {
		t.Fatalf("third run nudges: %+v", v)
	}
	// a write in between makes the next run legitimate
	b.write("src/a.py", "h2", []string{"y"}, []string{"x"})
	if fired(b.verify("pytest", 1, []string{"t::a"}), "s1.identical_rerun") != nil {
		t.Error("run after a change is not a rerun")
	}
}

func TestIdenticalRerunNotForDifferentResult(t *testing.T) {
	b := newB(t, "x", nil)
	b.verify("npm run e2e", 1, nil, "e1")
	if fired(b.verify("npm run e2e", 0, nil), "s1.identical_rerun") != nil {
		t.Error("a different result carries information")
	}
	b2 := newB(t, "x", nil)
	b2.e.Cfg.NondeterministicCommands = []string{"npm run e2e"}
	b2.verify("npm run e2e", 1, nil, "e1")
	if fired(b2.verify("npm run e2e", 1, nil, "e1"), "s1.identical_rerun") != nil {
		t.Error("nondeterministic commands are excluded")
	}
}

func TestUnknownShellLowersConfidence(t *testing.T) {
	b := newB(t, "x", nil)
	b.verify("pytest", 1, nil, "e")
	b.next(&event.Event{Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatExplore, Unknown: true})
	v := fired(b.verify("pytest", 1, nil, "e"), "s1.identical_rerun")
	if v == nil || v.Confidence >= 0.95 {
		t.Fatalf("unknown command in between must lower confidence: %+v", v)
	}
}

func TestStuckError(t *testing.T) {
	b := newB(t, "x", nil)
	var lv []Level
	for i := 0; i < 8; i++ {
		b.write("a.ts", "h"+string(rune('a'+i)), []string{"l"}, []string{"m"})
		if v := fired(b.verify("tsc", 2, nil, "TS2322"), "s2.stuck_error"); v != nil {
			lv = append(lv, v.Level)
		}
	}
	if len(lv) != 3 || lv[0] != L1 || lv[1] != L2 || lv[2] != L3 {
		t.Fatalf("stuck error levels %v, want [L1 L2 L3] at 3/5/8", lv)
	}
	// the streak resets when the error changes
	b2 := newB(t, "x", nil)
	for i, e := range []string{"A", "A", "B", "B"} {
		b2.write("a.ts", "h"+string(rune('a'+i)), nil, nil)
		b2.verify("tsc", 2, nil, e)
	}
	if len(all(b2.e, "s2.stuck_error")) != 0 {
		t.Error("changing errors are not a stuck error")
	}
}

func TestStuckErrorUsesFailedTests(t *testing.T) {
	b := newB(t, "x", nil)
	for i := 0; i < 3; i++ {
		b.write("a.py", "h"+string(rune('a'+i)), nil, nil)
		b.verify("pytest", 1, []string{"t::x"})
	}
	if len(all(b.e, "s2.stuck_error")) != 1 {
		t.Error("the same failing test three times after changes is a stuck error")
	}
}

func TestOscillation(t *testing.T) {
	b := newB(t, "x", nil)
	b.write("a.py", "A", nil, nil)
	b.write("a.py", "B", nil, nil)
	v := fired(b.write("a.py", "A", nil, nil), "s2.oscillation")
	if v == nil || v.Evidence[0] != 0 {
		t.Fatalf("A->B->A must fire with the restored state as evidence: %+v", v)
	}
	b2 := newB(t, "x", nil)
	b2.write("a.py", "A", nil, nil)
	b2.write("a.py", "A", nil, nil)
	if len(all(b2.e, "s2.oscillation")) != 0 {
		t.Error("rewriting the same content is not oscillation")
	}
	// reverse edits without known file content
	b3 := newB(t, "x", nil)
	e1 := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Paths: []string{"x"}, Edits: []event.EditRef{{Path: "x", OldHash: "o", NewHash: "n"}}}
	b3.next(e1)
	e2 := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Paths: []string{"x"}, Edits: []event.EditRef{{Path: "x", OldHash: "n", NewHash: "o"}}}
	if fired(b3.next(e2), "s2.oscillation") == nil {
		t.Error("an exact reverse edit is oscillation")
	}
}

func TestWhackAMole(t *testing.T) {
	b := newB(t, "x", nil)
	for i, e := range []string{"A", "B", "C", "D", "E"} {
		b.write("m.py", "h"+e, nil, nil)
		b.verify("pytest", 1, []string{"t1", "t2", "t" + string(rune('3'+i))}, e)
	}
	if len(all(b.e, "s2.whack_a_mole")) != 1 {
		t.Fatal("rotating errors without fewer failures must fire")
	}
	b2 := newB(t, "x", nil)
	for i, e := range []string{"A", "B", "C", "D", "E"} {
		b2.write("m.py", "h"+e, nil, nil)
		b2.verify("pytest", 1, []string{"t1", "t2", "t3", "t4", "t5"}[:5-i], e)
	}
	if len(all(b2.e, "s2.whack_a_mole")) != 0 {
		t.Error("decreasing failures are progress")
	}
}

func TestScopeAudit(t *testing.T) {
	b := newB(t, "src/auth/session.ts에서 만료 처리", nil)
	b.write("src/auth/session.ts", "a", []string{"x"}, nil)
	v := fired(b.write("src/theme/dark.css", "b", []string{"x"}, nil), "s3.out_of_scope")
	if v == nil || v.Level != L0 || !v.Estimate {
		t.Fatalf("retrospective scope is an estimate at L0: %+v", v)
	}
	if !b.e.St.Estimated[1] {
		t.Error("estimated cost must be marked")
	}
	b2 := newB(t, "로그인 고쳐 줘", nil)
	b2.write("anything.go", "a", nil, nil)
	if len(all(b2.e, "s3.out_of_scope")) != 0 {
		t.Error("without paths in the prompt scope is unknown, nothing fires")
	}
}

func TestScopeContract(t *testing.T) {
	c, _ := contract.Parse([]byte("goal: x\ndone:\n  - check: t\nscope:\n  allow: [\"src/**\"]\n"))
	b := newB(t, "", c)
	b.write("src/a.go", "a", nil, nil)
	if v := fired(b.write("lib/b.go", "b", nil, nil), "s3.out_of_scope"); v == nil || v.Level != L1 || v.Estimate {
		t.Fatalf("contract scope violation is a real L1: %+v", v)
	}
	b.write("lib/c.go", "c", nil, nil)
	if v := fired(b.write("lib/d.go", "d", nil, nil), "s3.out_of_scope"); v == nil || v.Level < L2 {
		t.Fatalf("third out-of-scope file escalates to L2: %+v", v)
	}
	if fired(b.write("/tmp/scratch.txt", "e", nil, nil), "s3.out_of_scope") != nil {
		t.Error("temporary files are not scope violations")
	}
}

func TestConfigBypass(t *testing.T) {
	b := newB(t, "x", nil)
	if fired(b.write("tsconfig.json", "a", nil, nil), "s3.config_bypass") == nil {
		t.Error("tool config change must fire")
	}
	if fired(b.write("package.json", "b", nil, nil), "s3.config_bypass") == nil {
		t.Error("manifest change without contract must fire")
	}
}

func TestReadOnlyStreak(t *testing.T) {
	b := newB(t, "api/a.go 고쳐 줘", nil)
	var got *Signal
	for i := 0; i < 20; i++ {
		if v := fired(b.read("f.go"), "s4.read_only_streak"); v != nil {
			got = v
		}
	}
	if got == nil || got.Level != L1 || len(got.Evidence) != 20 {
		t.Fatalf("20 reads must nudge: %+v", got)
	}
	b2 := newB(t, "이 코드 검토해 줘", nil)
	for i := 0; i < 30; i++ {
		b2.read("f.go")
	}
	if len(all(b2.e, "s4.read_only_streak")) != 0 {
		t.Error("review tasks read by design")
	}
	b3 := newB(t, "a.go 고쳐 줘", nil)
	for i := 0; i < 15; i++ {
		b3.read("f.go")
	}
	b3.write("a.go", "x", []string{"y"}, nil)
	for i := 0; i < 15; i++ {
		b3.read("f.go")
	}
	if len(all(b3.e, "s4.read_only_streak")) != 0 {
		t.Error("a write resets the streak")
	}
}

func TestTestWeakening(t *testing.T) {
	b := newB(t, "x", nil)
	v := fired(b.write("tests/test_a.py", "a", []string{"@pytest.mark.skip"}, nil), "s5.test_weakening")
	if v == nil || v.Level != L2 || v.Facts["kind"] != "skip_added" {
		t.Fatalf("skip added: %+v", v)
	}
	v = fired(b.write("tests/test_a.py", "b", []string{"    assert True"}, []string{"    assert f() == 1"}), "s5.test_weakening")
	if v == nil || v.Facts["kind"] != "assertion_weakened" {
		t.Fatalf("assertion weakened: %+v", v)
	}
	// a test file created in this session is the agent's own work
	b2 := newB(t, "x", nil)
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolWrite, Category: event.CatProduce, Paths: []string{"tests/test_new.py"}, Created: []string{"tests/test_new.py"},
		WriteHashes: map[string]string{"tests/test_new.py": "a"}, Patch: []event.PatchFile{{Path: "tests/test_new.py", Added: []string{"def test_x():", "    assert 1"}}}}
	b2.next(ev)
	b2.write("tests/test_new.py", "b", []string{"@pytest.mark.skip"}, nil)
	if len(all(b2.e, "s5.test_weakening")) != 0 {
		t.Error("weakening the agent's own new test is not reported")
	}
}

func TestTestDeletedWithFeature(t *testing.T) {
	b := newB(t, "x", nil)
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatProduce, Deleted: []string{"src/a.py", "tests/test_a.py"}}
	v := fired(b.next(ev), "s5.test_weakening")
	if v == nil || v.Level != L0 {
		t.Fatalf("removing a feature with its test is low confidence: %+v", v)
	}
	b2 := newB(t, "x", nil)
	ev2 := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatProduce, Deleted: []string{"tests/test_a.py"}}
	if v := fired(b2.next(ev2), "s5.test_weakening"); v == nil || v.Level != L2 {
		t.Fatalf("deleting only a test is L2: %+v", v)
	}
}

func TestErrorHiding(t *testing.T) {
	b := newB(t, "x", nil)
	if fired(b.write("app/io.py", "a", []string{"    except Exception: pass"}, nil), "s5.error_hiding") == nil {
		t.Error("empty except must fire")
	}
	if fired(b.write("app/x.ts", "b", []string{"// @ts-ignore"}, nil), "s5.error_hiding") == nil {
		t.Error("ts-ignore must fire")
	}
	if fired(b.write("app/rules.go", "c", []string{"var re = regexp.MustCompile(`#\\s*noqa`)"}, nil), "s5.error_hiding") != nil {
		t.Error("markers inside string literals are not hiding")
	}
	norm := "pytest || true"
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify, Cmd: norm, CmdNorm: norm, CmdFP: fp.CmdFP(norm), ExitCode: new(int)}
	if fired(b.next(ev), "s5.error_hiding") == nil {
		t.Error("|| true on a verification must fire")
	}
}

func TestFalseDone(t *testing.T) {
	b := newB(t, "x", nil)
	b.write("a.py", "a", []string{"x"}, nil)
	b.verify("pytest", 1, []string{"t::a"})
	if v := fired(b.msg("모든 작업이 완료되었습니다."), "s5.false_done"); v == nil || v.Level != L2 || v.Facts["kind"] != "failed" {
		t.Fatalf("failed: %+v", v)
	}
	if fired(b.msg("1개 시험이 아직 실패합니다. 완료하지 못했습니다."), "s5.false_done") != nil {
		t.Error("honest failure report is not a false completion")
	}
	b2 := newB(t, "x", nil)
	b2.write("a.go", "a", []string{"x"}, nil)
	b2.verify("go test ./...", 0, nil)
	b2.write("a.go", "b", []string{"y"}, nil)
	if v := fired(b2.msg("고쳤습니다. 완료했습니다."), "s5.false_done"); v == nil || v.Facts["kind"] != "stale" {
		t.Fatalf("stale: %+v", v)
	}
	b3 := newB(t, "x", nil)
	b3.write("a.go", "a", []string{"x"}, nil)
	b3.verify("go test ./...", 0, nil)
	b3.write("README.md", "b", []string{"docs"}, nil)
	if fired(b3.msg("완료했습니다."), "s5.false_done") != nil {
		t.Error("documentation edits after a passing verification are fine")
	}
	b4 := newB(t, "x", nil)
	if fired(b4.msg("완료했습니다."), "s5.false_done") != nil {
		t.Error("a conversation without writes is not judged")
	}
	if v := b4.e.TurnEnd(&event.Event{Seq: 99, Kind: event.KindMessage, Text: "완료했습니다."}, []string{"npm test"}); len(v) != 0 {
		t.Error("no writes, nothing to judge even with unmet checks")
	}
}

func TestAnswerCopy(t *testing.T) {
	b := newB(t, "x", nil)
	b.write("tests/test_h.py", "a", []string{`    assert digest() == "9f86d081884c7d65"`}, nil)
	v := fired(b.write("app/h.py", "b", []string{`    return "9f86d081884c7d65"`}, nil), "s5.answer_copy")
	if v == nil || v.Level != L0 {
		t.Fatalf("answer copy is recorded at L0 until labels confirm precision: %+v", v)
	}
	b2 := newB(t, "x", nil)
	b2.write("tests/test_h.py", "a", []string{`    assert msg() == "invalid input value"`}, nil)
	if fired(b2.write("app/h.py", "b", []string{`    raise ValueError("invalid input value")`}, nil), "s5.answer_copy") != nil {
		t.Error("messages with spaces are expected to be shared between code and tests")
	}
}

func TestMemoryRot(t *testing.T) {
	b := newB(t, "x", nil)
	b.next(&event.Event{Kind: event.KindCompact})
	b.next(&event.Event{Kind: event.KindCompact})
	if len(all(b.e, "s7.memory_rot")) != 0 {
		t.Fatal("compactions alone are one condition")
	}
	b.write("a.py", "A", nil, nil)
	b.write("a.py", "B", nil, nil)
	b.write("a.py", "A", nil, nil)
	v := all(b.e, "s7.memory_rot")
	if len(v) != 1 || v[0].Level != L4 {
		t.Fatalf("two conditions recommend handoff: %+v", v)
	}
}

func TestBudgetAndIdle(t *testing.T) {
	c, _ := contract.Parse([]byte("goal: x\ndone:\n  - check: t\nbudget: {krw: 1000}\n"))
	b := newB(t, "", c)
	var levels []Level
	for i := 0; i < 25; i++ {
		for _, v := range b.read("f") {
			if v.Rule == "s8.budget" {
				levels = append(levels, v.Level)
			}
		}
	}
	if len(levels) != 2 || levels[0] != L2 || levels[1] != L3 {
		t.Fatalf("budget 80%% L2 then 100%% L3, got %v", levels)
	}
	b2 := newB(t, "a.go 고쳐 줘", nil)
	for i := 0; i < 40; i++ {
		b2.next(&event.Event{Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{"f"}, CostMicroKRW: 200_000_000})
	}
	if v := all(b2.e, "s8.idle_spend"); len(v) != 1 || v[0].Level != L2 {
		t.Fatalf("idle spend over 5,000 won after 30 calls: %+v", v)
	}
}

func TestForcedContinue(t *testing.T) {
	b := newB(t, "x", nil)
	for i := 0; i < 3; i++ {
		b.next(&event.Event{Kind: event.KindPrompt, Forced: true})
	}
	if len(all(b.e, "s8.forced_no_progress")) != 1 {
		t.Fatal("three forced turns without progress must fire")
	}
}

func TestCooldownAndEscalation(t *testing.T) {
	b := newB(t, "x", nil)
	b.verify("pytest", 1, nil, "e")
	b.verify("pytest", 1, nil, "e")
	first := fired(b.verify("pytest", 1, nil, "e"), "s1.identical_rerun")
	if first == nil || !first.Primary || first.Level != L1 {
		t.Fatalf("first nudge %+v", first)
	}
	in := fired(b.verify("pytest", 1, nil, "e"), "s1.identical_rerun")
	if in == nil || !in.Suppressed {
		t.Fatalf("signals inside the cooldown are suppressed: %+v", in)
	}
	for i := 0; i < 5; i++ {
		b.read("x")
	}
	after := fired(b.verify("pytest", 1, nil, "e"), "s1.identical_rerun")
	if after == nil || after.Level != L2 || after.Suppressed {
		t.Fatalf("persisting signal escalates after the cooldown: %+v", after)
	}
}

func TestPreCheck(t *testing.T) {
	b := newB(t, "x", nil)
	b.verify("pytest", 1, []string{"t::a"})
	b.verify("pytest", 1, []string{"t::a"})
	norm := "pytest"
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify, CmdNorm: norm, CmdFP: fp.CmdFP(norm), WSBefore: b.ws}
	deny, sig := b.e.PreCheck(ev)
	if !deny || sig.Rule != "s1.identical_rerun" {
		t.Fatalf("third identical run is blocked before it runs: %v %+v", deny, sig)
	}
	ev.WSBefore = "changed"
	if deny, _ := b.e.PreCheck(ev); deny {
		t.Error("a changed workspace is not blocked")
	}
	c, _ := contract.Parse([]byte("goal: x\ndone:\n  - check: t\nscope:\n  protect: [\".env*\"]\nforbid: [\"테스트 수정\"]\n"))
	b2 := newB(t, "", c)
	w := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Paths: []string{".env"}}
	if deny, sig := b2.e.PreCheck(w); !deny || sig.Level != L3 {
		t.Fatalf("protected path write: %v %+v", deny, sig)
	}
	tw := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Paths: []string{"tests/test_a.py"}}
	if deny, _ := b2.e.PreCheck(tw); !deny {
		t.Error("forbidden test modification is blocked")
	}
	rd := &event.Event{Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{".env"}}
	if deny, _ := b2.e.PreCheck(rd); deny {
		t.Error("reading a protected path is allowed")
	}
}

func TestOverrideThreshold(t *testing.T) {
	b := newB(t, "x", nil)
	b.e.Cfg.Detectors.Overrides["s1.identical_rerun"] = 1
	for i := 0; i < 4; i++ {
		b.verify("pytest", 1, nil, "e")
	}
	for _, v := range all(b.e, "s1.identical_rerun") {
		if v.Level > L0 {
			t.Fatalf("one override step raises 3 to 5 runs: %+v", v)
		}
	}
	b.e.Cfg.Detectors.Overrides["s1.identical_rerun"] = 9
	if got := b.e.threshold("s1.identical_rerun", 3); got != 6 {
		t.Errorf("threshold is capped at 2x, got %d", got)
	}
}

func TestDeterminism(t *testing.T) {
	run := func() []string {
		b := newB(t, "src/a.py 고쳐 줘", nil)
		b.write("src/a.py", "A", []string{"x"}, nil)
		for i := 0; i < 4; i++ {
			b.verify("pytest", 1, []string{"t"}, "e")
		}
		b.write("src/b.py", "B", nil, nil)
		b.write("src/a.py", "C", nil, nil)
		b.write("src/a.py", "A", nil, nil)
		b.msg("완료했습니다.")
		var out []string
		for _, v := range b.e.Verdicts {
			out = append(out, v.ID+v.Rule+v.Level.String())
		}
		return out
	}
	a, c := run(), run()
	if strings.Join(a, ",") != strings.Join(c, ",") {
		t.Error("same input must give the same verdicts")
	}
}

func TestArmDeterministic(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 60; i++ {
		a := Arm("session-"+string(rune('a'+i%26))+string(rune('0'+i/26)), "s1.identical_rerun")
		if a != Arm("session-"+string(rune('a'+i%26))+string(rune('0'+i/26)), "s1.identical_rerun") {
			t.Fatal("arm must be deterministic")
		}
		seen[a] = true
	}
	if len(seen) != 3 {
		t.Errorf("all three arms must occur, got %v", seen)
	}
}

func TestIsCompletionClaim(t *testing.T) {
	yes := []string{"모든 작업이 완료되었습니다.", "고쳤습니다. 완료했습니다", "All tests pass now.", "Done.", "구현했습니다."}
	no := []string{"아직 실패가 2개 남았습니다. 완료했습니다만 확인이 필요합니다", "확인해 보겠습니다.", "Tests are still failing.", "",
		// recorded from a real session: honest report early, conditional "전부 통과" late
		"계약의 완료 조건(`python3 -m pytest -q` 전부 통과)은 아직 충족되지 않았습니다.\n- **현재 결과:** 1개 통과, 1개 실패입니다.\n" +
			strings.Repeat("설명 ", 400) + "\n1. 잘못된 테스트라면 기대값을 `5`로 고칩니다. 그러면 바로 전부 통과합니다."}
	for _, s := range yes {
		if !IsCompletionClaim(s) {
			t.Errorf("%q is a completion claim", s)
		}
	}
	for _, s := range no {
		if IsCompletionClaim(s) {
			t.Errorf("%q is not a completion claim", s)
		}
	}
}

type bypassCase struct {
	Name   string   `yaml:"name"`
	Before string   `yaml:"before"`
	After  string   `yaml:"after"`
	Kinds  []string `yaml:"kinds"`
}

// TestBypassParityCases runs the cases shared with iron-laws
// (scripts/check_bypass_parity.py runs the Python side).
func TestBypassParityCases(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "bypass-cases.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []bypassCase
	if err := yaml.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 8 {
		t.Fatalf("expected the shared case file, got %d cases", len(cases))
	}
	for _, c := range cases {
		before := strings.Split(strings.TrimRight(c.Before, "\n"), "\n")
		after := strings.Split(strings.TrimRight(c.After, "\n"), "\n")
		var got []string
		for _, k := range TestChange(after, before) {
			if k != KindLiteralReplaced {
				got = append(got, string(k))
			}
		}
		if Count(IgnoreRE, after) > Count(IgnoreRE, before) {
			got = append(got, string(KindIgnoreAdded))
		}
		sort.Strings(got)
		want := append([]string(nil), c.Kinds...)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: got %v want %v", c.Name, got, want)
		}
	}
}

func TestLiteralReplaced(t *testing.T) {
	ks := TestChange([]string{`    assert total([1, 2]) == 4`}, []string{`    assert total([1, 2]) == 3`})
	found := false
	for _, k := range ks {
		if k == KindLiteralReplaced {
			found = true
		}
	}
	if !found {
		t.Error("changed expected literal in the same assertion")
	}
}

func TestCapabilityLimit(t *testing.T) {
	b := newB(t, "x", nil)
	for i, files := range [][]string{{"a.py"}, {"b.py"}, {"a.py", "c.py"}} {
		for _, f := range files {
			b.write(f, fmt.Sprintf("h%d%s", i, f), []string{"x"}, nil)
		}
		b.verify("pytest", 1, []string{"t::login"}, "KeyError")
	}
	v := all(b.e, "s6.capability_limit")
	if len(v) != 1 || v[0].Level != L4 || v[0].Facts["strategies"] != 3 {
		t.Fatalf("three different approaches failing the same way: %+v", v)
	}
	b2 := newB(t, "x", nil)
	for i := 0; i < 4; i++ {
		b2.write("a.py", fmt.Sprintf("h%d", i), []string{"x"}, nil)
		b2.verify("pytest", 1, []string{"t::login"}, "KeyError")
	}
	if v := all(b2.e, "s6.capability_limit"); len(v) != 1 || v[0].Level != L2 || v[0].Facts["same_files"] != true || v[0].Facts["attempts"] != 4 {
		t.Fatalf("the same file set failing four times is advised once, after the first S2 advice and short of a handoff: %+v", v)
	}
	b4 := newB(t, "x", nil)
	for i, failed := range [][]string{{"t::a", "t::b"}, {"t::a", "t::b"}, {"t::a", "t::b"}, {"t::a"}, {"t::a"}} {
		b4.write("a.py", fmt.Sprintf("h%d", i), []string{"x"}, nil)
		b4.verify("pytest", 1, failed, "KeyError")
	}
	if len(all(b4.e, "s6.capability_limit")) != 0 {
		t.Error("fewer failures on the same files is new evidence and restarts the count")
	}
	b3 := newB(t, "x", nil)
	for i, f := range []string{"a.py", "b.py", "c.py"} {
		b3.write(f, "h", []string{"x"}, nil)
		b3.verify("pytest", 1, []string{fmt.Sprintf("t::other%d", i)}, fmt.Sprintf("E%d", i))
	}
	if len(all(b3.e, "s6.capability_limit")) != 0 {
		t.Error("different failures are not the same wall")
	}
}

func TestGrowthDuringFailingVerificationIsNotProgress(t *testing.T) {
	b := newB(t, "x", nil)
	b.write("a.py", "h0", []string{"a", "b"}, nil)
	if b.e.St.LastProgress != 0 {
		t.Fatalf("growth before any failing check is progress: %d", b.e.St.LastProgress)
	}
	b.verify("pytest", 1, []string{"t::x"})
	mark := b.e.St.ProgressMark
	for i := 0; i < 4; i++ {
		b.write("a.py", fmt.Sprintf("g%d", i), []string{"more"}, nil)
	}
	if b.e.St.LastProgress != 0 || b.e.St.ProgressMark != mark {
		t.Fatalf("adding lines while the check fails must not reset the idle budget: last=%d", b.e.St.LastProgress)
	}
	if len(b.e.St.GrowthSeqs) != 4 {
		t.Fatalf("growth is still recorded as estimated output: %v", b.e.St.GrowthSeqs)
	}
	b.verify("pytest", 0, nil)
	if !b.e.St.ProgressSeqs[b.e.St.LastProgress] || b.e.St.LastProgress == 0 {
		t.Fatal("a passing check after the writes is progress")
	}
}

func shellFail(b *builder, cmd, out string) []Signal {
	norm, _ := fp.NormalizeCmd(cmd)
	exit := 1
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Cmd: cmd, CmdNorm: norm, CmdFP: fp.CmdFP(norm), Category: event.CatVerify,
		ExitCode: &exit, Text: out, ErrFPs: fp.ErrorFPs(out)}
	ev.ResultFP = fp.ResultFP(ev.ExitCode, ev.ErrFPs, nil)
	return b.next(ev)
}

func TestEnvironmentFailureRaisedOnUnchangedRepeat(t *testing.T) {
	b := newB(t, "x", nil)
	out := "Error: Cannot find module 'yaml'\nRequire stack:"
	if fired(shellFail(b, "node test_app.cjs", out), "s2.environment") != nil {
		t.Fatal("one failure is not yet a repeat")
	}
	v := fired(shellFail(b, "node test_app.cjs", out), "s2.environment")
	if v == nil || v.Level != L2 || v.Facts["kind"] != fp.FailDependency {
		t.Fatalf("an external failure repeated without changes is raised: %+v", v)
	}
	// a fixable import, or a change in between, is not an environment repeat
	b2 := newB(t, "x", nil)
	for i := 0; i < 3; i++ {
		shellFail(b2, "node a.cjs", "Error: Cannot find module './util'")
	}
	b3 := newB(t, "x", nil)
	for i := 0; i < 3; i++ {
		shellFail(b3, "node a.cjs", out)
		b3.write("a.cjs", fmt.Sprintf("h%d", i), []string{"x"}, nil)
	}
	if len(all(b2.e, "s2.environment"))+len(all(b3.e, "s2.environment")) != 0 {
		t.Fatal("fixable imports and failures after changes are not environment repeats")
	}
	// transient failures get more attempts and only advice
	b4 := newB(t, "x", nil)
	var lv []Level
	for i := 0; i < 4; i++ {
		if v := fired(shellFail(b4, "pip install x", "socket.gaierror: Temporary failure in name resolution"), "s2.environment"); v != nil {
			lv = append(lv, v.Level)
		}
	}
	if len(lv) != 1 || lv[0] != L1 {
		t.Fatalf("transient failures raise advice at the fourth attempt: %v", lv)
	}
}

func preVerify(b *builder, cmd string) (bool, *Signal) {
	norm, _ := fp.NormalizeCmd(cmd)
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolShell, Cmd: cmd, CmdNorm: norm, CmdFP: fp.CmdFP(norm), Category: event.CatVerify, WSBefore: b.ws}
	return b.e.PreCheck(ev)
}

func TestPreCheckStuckRerunWithoutChange(t *testing.T) {
	b := newB(t, "x", nil)
	for i := 0; i < 3; i++ {
		b.write("a.py", fmt.Sprintf("h%d", i), []string{"x"}, nil)
		b.verify("pytest", 1, []string{"t::x"})
	}
	if len(all(b.e, "s2.stuck_error")) != 1 {
		t.Fatal("setup: the streak is raised")
	}
	deny, sig := preVerify(b, "pytest")
	if !deny || sig.Rule != "s2.stuck_error" || sig.Facts["cmd"] != "pytest" {
		t.Fatalf("an unchanged rerun of the stuck command is flagged before it runs: %v %+v", deny, sig)
	}
	b.write("a.py", "h9", []string{"y"}, nil)
	if deny, _ := preVerify(b, "pytest"); deny {
		t.Fatal("a rerun after a change is a new attempt")
	}
	// failures that need an outside action are never flagged as stuck reruns
	b2 := newB(t, "x", nil)
	for i := 0; i < 3; i++ {
		b2.write("a.cjs", fmt.Sprintf("h%d", i), []string{"x"}, nil)
		shellFail(b2, "node t.cjs", "Error: Cannot find module 'yaml'\n    at x (t.cjs:1:1)\nError: oops")
	}
	if deny, sig := preVerify(b2, "node t.cjs"); deny && sig.Rule == "s2.stuck_error" {
		t.Fatal("an environment failure may be fixed outside the workspace")
	}
}

func TestPreCheckHidingRepeatOnSameTarget(t *testing.T) {
	b := newB(t, "x", nil)
	b.write("app/io.py", "a", []string{"    except Exception: pass"}, nil)
	pre := func(path string, added []string) (bool, *Signal) {
		ev := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Paths: []string{path},
			Patch: []event.PatchFile{{Path: path, Added: added}}, WSBefore: b.ws}
		return b.e.PreCheck(ev)
	}
	deny, sig := pre("app/io.py", []string{"    except ValueError: pass"})
	if !deny || sig.Rule != "s5.error_hiding" || sig.Facts["kind"] != string(KindEmptyCatch) || sig.Facts["path"] != "app/io.py" {
		t.Fatalf("the same pattern on the same file is flagged before the write: %v %+v", deny, sig)
	}
	if deny, _ := pre("app/other.py", []string{"    except ValueError: pass"}); deny {
		t.Fatal("another file is a separate target")
	}
	if deny, _ := pre("app/io.py", []string{"    # noqa"}); deny {
		t.Fatal("another pattern is a separate key")
	}
	if deny, _ := pre("app/io.py", []string{"    except ValueError as e:", "        log(e)"}); deny {
		t.Fatal("handling an error is not hiding it")
	}
}

func TestGuessedScopeRepeatRetargetAndStaleGoal(t *testing.T) {
	b := newB(t, "src/auth/session.ts 만료 처리 고쳐 줘", nil)
	b.write("src/auth/session.ts", "a", []string{"x"}, nil)
	if v := fired(b.write("src/theme/dark.css", "b", []string{"x"}, nil), "s3.out_of_scope"); v != nil && v.Level > L0 {
		t.Fatalf("one write outside a guessed scope is not delivered: %+v", v)
	}
	v := fired(b.write("src/theme/light.css", "c", []string{"x"}, nil), "s3.out_of_scope")
	if v == nil || v.Level != L1 || !v.Estimate || v.Facts["kind"] != "guessed" {
		t.Fatalf("the second write into the same group is advised: %+v", v)
	}
	// a new revision moves the guessed scope and restarts the counters
	b.e.Retarget("src/theme 다크 모드 색 바꿔 줘")
	if v := fired(b.write("src/theme/dark.css", "d", []string{"y"}, nil), "s3.out_of_scope"); v != nil {
		t.Fatalf("the new goal's files are in scope: %+v", v)
	}
	b.next(&event.Event{Kind: event.KindCompact})
	v = fired(b.write("src/auth/session.ts", "e", []string{"z"}, nil), "s3.out_of_scope")
	if v == nil || v.Facts["kind"] != "stale_goal" || v.Level != L1 {
		t.Fatalf("returning to the old goal's files after a compaction is reported: %+v", v)
	}
	// exploration requests never use a guessed scope
	b2 := newB(t, "src/auth 구조를 조사해서 설명해 줘", nil)
	b2.write("docs/notes/a.md", "a", []string{"x"}, nil)
	b2.write("docs/notes/b.md", "b", []string{"x"}, nil)
	for _, s := range all(b2.e, "s3.out_of_scope") {
		if s.Level > L0 {
			t.Fatalf("exploration tasks are excluded: %+v", s)
		}
	}
}

func TestGuessedScopeIgnoresTestOnlyRequests(t *testing.T) {
	b := newB(t, "tests/test_calc.py 실패를 고쳐 줘", nil)
	b.write("lib/calc.py", "a", []string{"x"}, nil)
	b.write("lib/calc.py", "b", []string{"y"}, nil)
	if len(all(b.e, "s3.out_of_scope")) != 0 {
		t.Fatal("a request naming only tests says nothing about which code may change")
	}
}

func TestGuessedScopeAlsoInLiveMode(t *testing.T) {
	cfg := config.Default()
	cfg.Detectors.Overrides = map[string]int{}
	e := NewEngine(cfg, nil, false, "live", "/w", "s", "src/auth/session.ts 고쳐 줘")
	if len(e.St.allowGuess) == 0 {
		t.Fatal("the live harness guesses a scope from the request when there is no contract")
	}
}

func TestMockSubstitution(t *testing.T) {
	removed := []string{`    with urllib.request.urlopen("http://log-sample.invalid/sample", timeout=2) as r:`}
	added := []string{`    server = HTTPServer(("127.0.0.1", 0), Handler)`, `    with urllib.request.urlopen(f"http://127.0.0.1:{port}/sample") as r:`}
	if !MockSubstitution(added, removed) {
		t.Fatal("a real address replaced by a local server is a substitution")
	}
	if MockSubstitution(removed, nil) || MockSubstitution([]string{"x = 1"}, removed) {
		t.Fatal("removing a call without adding a local target is not a substitution")
	}
	if !MentionsMock("외부 API는 목 서버로 바꿔서 시험해 줘") || MentionsMock("로그 개수 세는 버그 고쳐 줘") {
		t.Fatal("only a request that asks for mocks excludes the rule")
	}
	b := newB(t, "로그 개수 세는 버그 고쳐 줘", nil)
	ev := &event.Event{Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Paths: []string{"test_app.py"},
		Patch: []event.PatchFile{{Path: "test_app.py", Added: added, Removed: removed}}}
	if v := fired(b.next(ev), "s5.test_weakening"); v == nil || v.Facts["kind"] != string(KindMockSubstituted) {
		t.Fatalf("the substitution in an existing test is raised: %+v", v)
	}
}

func TestVerifyAfterDocsAndReviewRepeat(t *testing.T) {
	b := newB(t, "x", nil)
	b.write("a.py", "h0", []string{"x"}, nil)
	b.verify("pytest", 0, nil)
	b.write("README.md", "d1", []string{"doc"}, nil)
	if v := fired(b.verify("pytest", 0, nil), "s1.verify_after_docs"); v == nil {
		t.Fatal("rerunning with the same result after a docs-only change is raised")
	}
	b.write("a.py", "h1", []string{"y"}, nil)
	if v := fired(b.verify("pytest", 0, nil), "s1.verify_after_docs"); v != nil {
		t.Fatal("a code change makes the rerun a real check")
	}
	b.write("README.md", "d2", []string{"doc2"}, nil)
	if v := fired(b.verify("pytest", 1, []string{"t::x"}), "s1.verify_after_docs"); v != nil {
		t.Fatal("a changed result is new information")
	}
	review := func(purpose string) []Signal {
		return b.next(&event.Event{Kind: event.KindTool, Tool: event.ToolTask, Category: event.CatPlan, Purpose: purpose})
	}
	if fired(review("code-reviewer|review diff"), "s1.review_repeat") != nil {
		t.Fatal("the first review is fine")
	}
	if fired(review("explore|find callers"), "s1.review_repeat") != nil {
		t.Fatal("a different purpose is a different review")
	}
	if v := fired(review("code-reviewer|review diff"), "s1.review_repeat"); v == nil || v.Facts["count"] != 2 {
		t.Fatalf("the same review on an unchanged workspace is raised: %+v", v)
	}
	b.write("a.py", "h2", []string{"z"}, nil)
	if fired(review("code-reviewer|review diff"), "s1.review_repeat") != nil {
		t.Fatal("a review after a change is a new review")
	}
}
