package live

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

type harness struct {
	t    *testing.T
	home string
	proj string
	db   string
	done chan error
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

func startDaemon(t *testing.T) *harness {
	t.Helper()
	base := t.TempDir()
	h := &harness{t: t, home: filepath.Join(base, "home"), proj: filepath.Join(base, "proj"), done: make(chan error, 1)}
	h.db = filepath.Join(h.home, "ledger.db")
	t.Setenv("SAMCHEONPO_HOME", h.home)
	t.Setenv("XDG_RUNTIME_DIR", base)
	t.Setenv("SAMCHEONPO_NO_SPAWN", "1")
	// Payload/capability tests use captured versions, not whichever agent CLI
	// happens to be installed on the test host. Cold probing is tested separately.
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for agent, version := range map[string]string{"claude": "2.1.0", "codex": "0.160.0", "opencode": "1.18.34"} {
		if err := os.WriteFile(filepath.Join(bin, agent), []byte("#!/bin/sh\nprintf '%s\\n' '"+version+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.MkdirAll(filepath.Join(h.proj, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(h.proj, "src", "a.py"), []byte("x = 1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(h.proj, ".samcheonpo.yml"), []byte("notify: {desktop: false}\nexperiment: {enabled: false}\n"), 0o644)
	git(t, h.proj, "init", "-q")
	git(t, h.proj, "add", "-A")
	git(t, h.proj, "commit", "-qm", "init")
	go func() { h.done <- Run(h.db, time.Minute) }()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, _, ok := hookclient.Query("Ping", map[string]string{}, time.Second); ok {
			break
		}
	}
	t.Cleanup(func() {
		hookclient.Query("Shutdown", map[string]string{}, time.Second)
		select {
		case <-h.done:
		case <-time.After(5 * time.Second):
		}
	})
	for _, agent := range []string{"claude", "codex", "opencode"} {
		ready := false
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			text, errText, ok := hookclient.Query("AgentStatus", map[string]string{"agent": agent}, time.Second)
			var state struct {
				Ready  bool
				Tested bool
			}
			if ok && errText == "" && json.Unmarshal([]byte(text), &state) == nil && state.Ready && state.Tested {
				ready = true
				break
			}
		}
		if !ready {
			t.Fatalf("fixture capability probe not ready: %s", agent)
		}
	}
	return h
}

// send delivers a hook event with a generous timeout and returns the hook output.
func (h *harness) send(event string, payload map[string]any) map[string]any {
	h.t.Helper()
	payload["hook_event_name"] = event
	if _, ok := payload["cwd"]; !ok {
		payload["cwd"] = h.proj
	}
	if _, ok := payload["session_id"]; !ok {
		payload["session_id"] = "sess-1"
	}
	conn, err := net.DialTimeout("unix", SocketPath(), time.Second)
	if err != nil {
		h.t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(150 * time.Second))
	p, _ := json.Marshal(payload)
	req, _ := json.Marshal(map[string]any{"v": 1, "agent": "claude", "event": event, "payload": json.RawMessage(p)})
	_, _ = conn.Write(append(req, '\n'))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		h.t.Fatal(err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		h.t.Fatal(err)
	}
	if resp.Error != "" {
		h.t.Fatalf("%s: %s", event, resp.Error)
	}
	out := map[string]any{}
	if len(resp.Output) > 0 {
		_ = json.Unmarshal(resp.Output, &out)
	}
	return out
}

func ctxOf(out map[string]any) string {
	if h, ok := out["hookSpecificOutput"].(map[string]any); ok {
		if s, ok := h["additionalContext"].(string); ok {
			return s
		}
	}
	return ""
}

func (h *harness) shell(id, cmd, stdout string, exit int) map[string]any {
	in := map[string]any{"tool_name": "Bash", "tool_use_id": id, "tool_input": map[string]any{"command": cmd}}
	pre := h.send("PreToolUse", in)
	if hs, ok := pre["hookSpecificOutput"].(map[string]any); ok && hs["permissionDecision"] == "deny" {
		return pre
	}
	post := map[string]any{"tool_name": "Bash", "tool_use_id": id, "tool_input": map[string]any{"command": cmd},
		"tool_response": map[string]any{"stdout": stdout, "stderr": "", "interrupted": false}}
	if exit != 0 {
		post["error"] = fmt.Sprintf("Exit code %d\n%s", exit, stdout)
		return h.send("PostToolUseFailure", post)
	}
	return h.send("PostToolUse", post)
}

func TestHookFlowIdenticalRerun(t *testing.T) {
	h := startDaemon(t)
	h.declareTestRules("s1.identical_rerun")
	h.send("SessionStart", map[string]any{"source": "startup"})
	out := h.send("UserPromptSubmit", map[string]any{"prompt": "src/a.py 시험 통과시켜 줘"})
	if ctxOf(out) != "" {
		t.Fatalf("observation-only by default: nothing is injected on the first prompt: %v", out)
	}
	var nudges []string
	var denied map[string]any
	for i := 0; i < 5; i++ {
		o := h.shell(fmt.Sprintf("t%d", i), "pytest -q", "FAILED tests/test_a.py::test_x - assert 1 == 2\n1 failed", 1)
		if c := ctxOf(o); c != "" {
			nudges = append(nudges, c)
		}
		if hs, ok := o["hookSpecificOutput"].(map[string]any); ok && hs["permissionDecision"] == "deny" {
			denied = hs
			break
		}
		time.Sleep(150 * time.Millisecond) // let the worker refresh the fingerprint
	}
	// the third identical run is the L1 intervention: blocked before it runs
	// with the previous result; nothing was nudged before that
	if len(nudges) != 0 {
		t.Errorf("no nudge before the third run: %v", nudges)
	}
	if denied == nil {
		t.Fatal("the third identical run must be blocked")
	}
	reason := denied["permissionDecisionReason"].(string)
	if !strings.Contains(reason, "[삼천포] 관찰:") || !strings.Contains(reason, "pytest -q") || !strings.Contains(reason, "test_a.py::test_x") {
		t.Fatalf("block reason carries the observation and the previous result: %s", reason)
	}
	// an edit changes the workspace: the next run is allowed
	_ = os.WriteFile(filepath.Join(h.proj, "src", "a.py"), []byte("x = 2\n"), 0o644)
	h.send("PreToolUse", map[string]any{"tool_name": "Edit", "tool_use_id": "e1", "tool_input": map[string]any{"file_path": filepath.Join(h.proj, "src", "a.py"), "old_string": "x = 1", "new_string": "x = 2"}})
	h.send("PostToolUse", map[string]any{"tool_name": "Edit", "tool_use_id": "e1", "tool_input": map[string]any{"file_path": filepath.Join(h.proj, "src", "a.py"), "old_string": "x = 1", "new_string": "x = 2"},
		"tool_response": map[string]any{"filePath": filepath.Join(h.proj, "src", "a.py")}})
	time.Sleep(300 * time.Millisecond)
	o := h.shell("t9", "pytest -q", "1 passed", 0)
	if hs, ok := o["hookSpecificOutput"].(map[string]any); ok && hs["permissionDecision"] == "deny" {
		t.Fatal("a run after an edit must not be blocked")
	}
	line, _, ok := hookclient.Query("Statusline", map[string]string{"session_id": "sess-1"}, time.Second)
	if !ok || !strings.Contains(line, "공회전") || !strings.Contains(line, "헛짓") {
		t.Fatalf("statusline %q", line)
	}
	end := h.send("SessionEnd", map[string]any{})
	msg, _ := end["systemMessage"].(string)
	p := strings.TrimPrefix(msg, "삼천포 영수증: ")
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), "삼천포 영수증") {
		t.Fatalf("receipt file %q: %v", p, err)
	}
	time.Sleep(100 * time.Millisecond)
	db, err := ledger.Open(h.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.Mode("sess-1") != "live" {
		t.Error("live session must be in the ledger")
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM intervention WHERE session_id='sess-1'`).Scan(&n)
	if n == 0 {
		t.Error("delivered interventions must be recorded")
	}
	if _, err := db.Seal("sess-1"); err != nil {
		t.Error("live sessions are sealed at SessionEnd")
	}
}

func TestContractFlowAndStop(t *testing.T) {
	h := startDaemon(t)
	h.declareTestRules("s5.stop_unmet")
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/a.py 고쳐 줘"})
	_ = os.MkdirAll(contract.Dir(h.proj), 0o755)
	// invalid draft gets validation feedback
	_ = os.WriteFile(contract.Path(h.proj), []byte("goal: x\n"), 0o644)
	w := map[string]any{"tool_name": "Write", "tool_use_id": "w1", "tool_input": map[string]any{"file_path": contract.Path(h.proj), "content": "goal: x\n"}}
	h.send("PreToolUse", w)
	w["tool_response"] = map[string]any{"type": "create"}
	out := h.send("PostToolUse", w)
	if !strings.Contains(ctxOf(out), "done") {
		t.Fatalf("validation errors must be returned: %v", out)
	}
	// a valid draft over the confirmation threshold shows the goal card to the
	// user without ending the agent's turn; the draft authorizes nothing
	valid := "goal: a.py의 x를 2로 만든다\ndone:\n  - check: \"grep -q 'x = 2' src/a.py\"\nbudget: {krw: 5000}\n"
	_ = os.WriteFile(contract.Path(h.proj), []byte(valid), 0o644)
	w["tool_use_id"] = "w2"
	h.send("PreToolUse", w)
	out = h.send("PostToolUse", w)
	if _, stopped := out["continue"]; stopped {
		t.Fatalf("an agent-written draft must not end the turn: %v", out)
	}
	if sm, _ := out["systemMessage"].(string); !strings.Contains(sm, "AI는 이렇게 이해했습니다") || !strings.Contains(ctxOf(out), "원래 작업을 이어서") {
		t.Fatalf("the goal card goes to the user and the agent continues: %v", out)
	}
	if st := contract.LoadAcceptance(h.proj); st.State != contract.StateDraft {
		t.Fatalf("a shown card is not acceptance: %s", st.State)
	}
	text, errText, ok := hookclient.Query("Command", CommandInput{Name: "accept", Root: h.proj}, 5*time.Second)
	if !ok || errText != "" || !strings.Contains(text, "수락") {
		t.Fatalf("accept: %q %q %v", text, errText, ok)
	}
	if st := contract.LoadAcceptance(h.proj); st.State != contract.StateAccepted {
		t.Fatalf("state %s", st.State)
	}
	// stopping while the check fails is blocked, at most twice
	stop := h.send("Stop", map[string]any{"stop_hook_active": false, "last_assistant_message": "완료했습니다."})
	if stop["decision"] != "block" || !strings.Contains(stop["reason"].(string), "[삼천포] 관찰: 계약의 완료 조건 1개") {
		t.Fatalf("failing contract check must block the stop: %v", stop)
	}
	h.send("Stop", map[string]any{"stop_hook_active": false, "last_assistant_message": "완료했습니다."})
	third := h.send("Stop", map[string]any{"stop_hook_active": false, "last_assistant_message": "완료했습니다."})
	if third["decision"] == "block" {
		t.Fatal("no more than two stop blocks per session")
	}
	if sm, _ := third["systemMessage"].(string); !strings.Contains(sm, "완료 조건") {
		t.Errorf("the user is told the criteria failed: %v", third)
	}
	again := h.send("Stop", map[string]any{"stop_hook_active": true})
	if again["decision"] == "block" {
		t.Fatal("never block when another stop hook is active")
	}
	// once the check passes the stop goes through and progress is verified
	_ = os.WriteFile(filepath.Join(h.proj, "src", "a.py"), []byte("x = 2\n"), 0o644)
	ok2 := h.send("Stop", map[string]any{"stop_hook_active": false, "last_assistant_message": "완료했습니다."})
	if ok2["decision"] == "block" {
		t.Fatal("passing checks do not block")
	}
	if sm, _ := ok2["systemMessage"].(string); !strings.Contains(sm, "통과했습니다.") {
		t.Errorf("a newly met criterion is shown to the user: %v", ok2)
	}
	line, _, _ := hookclient.Query("Statusline", map[string]string{"session_id": "sess-1"}, time.Second)
	if !strings.Contains(line, "진척 1/1") {
		t.Errorf("statusline shows verified progress: %q", line)
	}
	summary, _, _ := hookclient.Query("Command", CommandInput{Name: "summary", Root: h.proj}, 5*time.Second)
	if !strings.Contains(summary, "완료 조건 1개 중 1개") {
		t.Errorf("summary %q", summary)
	}
}

func TestContractConfirmAlwaysStopsTurn(t *testing.T) {
	h := startDaemon(t)
	_ = os.WriteFile(filepath.Join(h.proj, ".samcheonpo.yml"), []byte("notify: {desktop: false}\nexperiment: {enabled: false}\ncontract: {confirm: always}\n"), 0o644)
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/a.py 고쳐 줘"})
	_ = os.MkdirAll(contract.Dir(h.proj), 0o755)
	valid := "goal: a.py의 x를 2로 만든다\ndone:\n  - check: \"grep -q 'x = 2' src/a.py\"\nbudget: {krw: 100}\n"
	_ = os.WriteFile(contract.Path(h.proj), []byte(valid), 0o644)
	w := map[string]any{"tool_name": "Write", "tool_use_id": "w1", "tool_input": map[string]any{"file_path": contract.Path(h.proj), "content": valid}}
	h.send("PreToolUse", w)
	w["tool_response"] = map[string]any{"type": "create"}
	out := h.send("PostToolUse", w)
	if out["continue"] != false || !strings.Contains(out["stopReason"].(string), "AI는 이렇게 이해했습니다") {
		t.Fatalf("confirm: always must stop the turn: %v", out)
	}
}

func TestIgnoredAdviceEscalatesToBlockUntilReleased(t *testing.T) {
	h := startDaemon(t)
	h.send("SessionStart", map[string]any{"source": "startup"})
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/a.py 시험 통과시켜 줘"})
	run := func(i int) (advice string, denied map[string]any) {
		o := h.shell(fmt.Sprintf("r%d", i), "pytest -q", "FAILED tests/test_a.py::test_x - assert 1 == 2\n1 failed", 1)
		if hs, ok := o["hookSpecificOutput"].(map[string]any); ok && hs["permissionDecision"] == "deny" {
			return "", hs
		}
		time.Sleep(150 * time.Millisecond)
		return ctxOf(o), nil
	}
	var advised bool
	var denied map[string]any
	for i := 0; i < 6 && denied == nil; i++ {
		advice, d := run(i)
		if d != nil {
			denied = d
			break
		}
		if strings.Contains(advice, "[삼천포] 관찰:") {
			advised = true
		}
	}
	if !advised {
		t.Fatal("an unvalidated rule advises before it blocks")
	}
	if denied == nil || !strings.Contains(denied["permissionDecisionReason"].(string), "권고가 이미 전달됐는데 반복") {
		t.Fatalf("a repeat after delivered advice must be blocked with the reason: %v", denied)
	}
	// the user releases the verdict they just saw
	text, errText, ok := hookclient.Query("Command", CommandInput{Name: "keep", Arg: "normal", Root: h.proj}, 5*time.Second)
	if !ok || errText != "" || !strings.Contains(text, "오탐") || !strings.Contains(text, "pytest -q") || !strings.Contains(text, "다른 대상") {
		t.Fatalf("keep normal states what it releases: %q %q %v", text, errText, ok)
	}
	for i := 10; i < 13; i++ {
		if _, d := run(i); d != nil {
			t.Fatalf("a verdict the user released must not be blocked again: %v", d)
		}
	}
	// the release covers that target only; another command is judged afresh
	other := func(i int) map[string]any {
		o := h.shell(fmt.Sprintf("o%d", i), "pytest -q tests/test_b.py", "FAILED tests/test_b.py::test_y - assert 0\n1 failed", 1)
		time.Sleep(150 * time.Millisecond)
		return o
	}
	var otherReason string
	for i := 0; i < 8 && otherReason == ""; i++ {
		if hs, ok := other(i)["hookSpecificOutput"].(map[string]any); ok && hs["permissionDecision"] == "deny" {
			otherReason, _ = hs["permissionDecisionReason"].(string)
			if i == 0 {
				t.Fatal("another target is advised before it is blocked")
			}
		}
	}
	if !strings.Contains(otherReason, "권고가 이미 전달됐는데 반복") {
		t.Fatalf("the release does not cover another target, which still escalates after advice: %q", otherReason)
	}
}

func TestDraftRequestKeepsAgentWorkingAndShowsCardToUser(t *testing.T) {
	h := startDaemon(t)
	_ = os.WriteFile(filepath.Join(h.proj, ".samcheonpo.yml"), []byte("notify: {desktop: false}\nexperiment: {enabled: false}\ncontract: {draft: on}\n"), 0o644)
	out := h.send("UserPromptSubmit", map[string]any{"prompt": "src/a.py 고쳐 줘"})
	agent := ctxOf(out)
	if !strings.Contains(agent, ".samcheonpo/contract.yml") || !strings.Contains(agent, "기다리지 말고") {
		t.Fatalf("the agent gets the draft request and keeps working: %q", agent)
	}
	if strings.Contains(agent, "/samcheonpo:accept") {
		t.Fatalf("the user command menu must not be injected into the agent: %q", agent)
	}
	if sm, _ := out["systemMessage"].(string); !strings.Contains(sm, "/samcheonpo:accept") {
		t.Fatalf("the user sees the goal card with its commands: %v", out)
	}
}

func TestIronLawsAdviceNeverBlocksTheNextWrite(t *testing.T) {
	h := startDaemon(t)
	fake := `#!/bin/sh
file="$2"; out=""
while [ $# -gt 0 ]; do [ "$1" = "-o" ] && out="$2"; shift; done
n=$(grep -c SWALLOW "$file" 2>/dev/null || true)
printf '{"violations":[' > "$out"
i=0; while [ "$i" -lt "${n:-0}" ]; do [ "$i" -gt 0 ] && printf ',' >> "$out"; printf '{"rule_id":"IL-301","iron_law":3,"line_number":1,"confidence":"CONFIRMED"}' >> "$out"; i=$((i+1)); done
printf ']}' >> "$out"
`
	if err := os.WriteFile(filepath.Join(filepath.Dir(h.home), "bin", "iron-laws"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/io.py 고쳐 줘"})
	write := func(id, rel, content string) map[string]any {
		abs := filepath.Join(h.proj, rel)
		in := map[string]any{"tool_name": "Write", "tool_use_id": id, "tool_input": map[string]any{"file_path": abs, "content": content}}
		pre := h.send("PreToolUse", in)
		if hs, ok := pre["hookSpecificOutput"].(map[string]any); ok && hs["permissionDecision"] == "deny" {
			return hs
		}
		_ = os.WriteFile(abs, []byte(content), 0o644)
		in["tool_response"] = map[string]any{"type": "create"}
		h.send("PostToolUse", in)
		return nil
	}
	if d := write("w1", "src/io.py", "SWALLOW = 1\n"); d != nil {
		t.Fatalf("the first write is only observed: %v", d)
	}
	// the asynchronous check raises the advice; the next hook delivers it
	deadline := time.Now().Add(5 * time.Second)
	delivered := false
	for i := 0; time.Now().Before(deadline) && !delivered; i++ {
		time.Sleep(100 * time.Millisecond)
		out := h.send("PreToolUse", map[string]any{"tool_name": "Read", "tool_use_id": fmt.Sprintf("r%d", i), "tool_input": map[string]any{"file_path": filepath.Join(h.proj, "src", "a.py")}})
		delivered = strings.Contains(ctxOf(out), "IL-301")
	}
	if !delivered {
		t.Fatal("the iron-laws advice reaches the agent")
	}
	// The external checker cannot finish within the hook client's wait, so
	// a repeat is reported after the write and never blocked before it.
	if d := write("w2", "src/io.py", "SWALLOW = 1\nSWALLOW = 2\n"); d != nil {
		t.Fatalf("a pre-write block nobody receives must not be issued: %v", d)
	}
}

func TestShadowRuleIsRecordedNotDelivered(t *testing.T) {
	h := startDaemon(t)
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/a.py 고쳐 줘"})
	h.shell("v1", "pytest -q", "1 passed", 0)
	time.Sleep(200 * time.Millisecond)
	readme := filepath.Join(h.proj, "README.md")
	_ = os.WriteFile(readme, []byte("doc\n"), 0o644)
	w := map[string]any{"tool_name": "Write", "tool_use_id": "d1", "tool_input": map[string]any{"file_path": readme, "content": "doc\n"}}
	h.send("PreToolUse", w)
	w["tool_response"] = map[string]any{"type": "create"}
	h.send("PostToolUse", w)
	time.Sleep(200 * time.Millisecond)
	out := h.shell("v2", "pytest -q", "1 passed", 0)
	time.Sleep(200 * time.Millisecond)
	next := h.send("PreToolUse", map[string]any{"tool_name": "Read", "tool_use_id": "r1", "tool_input": map[string]any{"file_path": filepath.Join(h.proj, "src", "a.py")}})
	for _, o := range []map[string]any{out, next} {
		if strings.Contains(ctxOf(o), "문서만 바뀐 뒤") {
			t.Fatalf("a shadow rule never reaches the agent: %v", o)
		}
	}
	h.send("SessionEnd", map[string]any{})
	time.Sleep(100 * time.Millisecond)
	db, err := ledger.Open(h.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var channel string
	_ = db.QueryRow(`SELECT i.channel FROM intervention i JOIN verdict v ON v.id = i.verdict_id WHERE i.session_id='sess-1' AND v.rule='s1.verify_after_docs'`).Scan(&channel)
	if channel != "shadow" {
		t.Fatalf("the shadow verdict is recorded for measurement: %q", channel)
	}
}

func TestEvidenceRerunIsAdvisedThenBlocked(t *testing.T) {
	h := startDaemon(t)
	_ = os.WriteFile(filepath.Join(h.proj, "runtime.txt"), []byte("py3\n"), 0o644)
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/a.py 고쳐 줘"})
	_ = os.MkdirAll(contract.Dir(h.proj), 0o755)
	c := "spec: progress-contract/2\ngoal: a.py의 x를 1로 유지\ndone:\n  - id: grep\n    check: \"grep -q 'x = 1' src/a.py\"\n    pure: true\n    reuse: {inputs: [src/a.py], environment_files: [runtime.txt], deterministic: true, max_age_sec: 600}\nbudget: {krw: 100}\n"
	_ = os.WriteFile(contract.Path(h.proj), []byte(c), 0o644)
	w := map[string]any{"tool_name": "Write", "tool_use_id": "w1", "tool_input": map[string]any{"file_path": contract.Path(h.proj), "content": c}}
	h.send("PreToolUse", w)
	w["tool_response"] = map[string]any{"type": "create"}
	h.send("PostToolUse", w)
	if text, errText, ok := hookclient.Query("Command", CommandInput{Name: "accept", Root: h.proj}, 5*time.Second); !ok || errText != "" {
		t.Fatalf("accept: %q %q", text, errText)
	}
	// a checkpoint produces the passing evidence
	h.send("Stop", map[string]any{"stop_hook_active": false, "last_assistant_message": "확인했습니다."})
	// an unrelated file changes the workspace but not the declared inputs
	_ = os.WriteFile(filepath.Join(h.proj, "notes.txt"), []byte("n\n"), 0o644)
	run := func(id string) map[string]any {
		return h.send("PreToolUse", map[string]any{"tool_name": "Bash", "tool_use_id": id, "tool_input": map[string]any{"command": "grep -q 'x = 1' src/a.py"}})
	}
	first := run("g1")
	if hs, _ := first["hookSpecificOutput"].(map[string]any); hs["permissionDecision"] == "deny" || !strings.Contains(ctxOf(first), "통과 근거") {
		t.Fatalf("the first rerun with valid evidence is advised, not blocked: %v", first)
	}
	h.send("PostToolUse", map[string]any{"tool_name": "Bash", "tool_use_id": "g1", "tool_input": map[string]any{"command": "grep -q 'x = 1' src/a.py"}, "tool_response": map[string]any{"stdout": ""}})
	second := run("g2")
	if hs, _ := second["hookSpecificOutput"].(map[string]any); hs["permissionDecision"] != "deny" || !strings.Contains(hs["permissionDecisionReason"].(string), "통과 근거") {
		t.Fatalf("repeating after the advice is blocked with the evidence: %v", second)
	}
	// changing a declared input makes the check worth running again
	_ = os.WriteFile(filepath.Join(h.proj, "src", "a.py"), []byte("x = 1\ny = 2\n"), 0o644)
	if hs, _ := run("g3")["hookSpecificOutput"].(map[string]any); hs["permissionDecision"] == "deny" {
		t.Fatal("a changed input invalidates the evidence")
	}
}

func TestSummaryQuotesRequestNextToCurrentWork(t *testing.T) {
	h := startDaemon(t)
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/auth/session.py 만료 처리 고쳐 줘"})
	for i, rel := range []string{"src/auth/session.py", "src/theme/dark.css"} {
		abs := filepath.Join(h.proj, rel)
		_ = os.MkdirAll(filepath.Dir(abs), 0o755)
		_ = os.WriteFile(abs, []byte("x\n"), 0o644)
		in := map[string]any{"tool_name": "Write", "tool_use_id": fmt.Sprintf("w%d", i), "tool_input": map[string]any{"file_path": abs, "content": "x\n"}}
		h.send("PreToolUse", in)
		in["tool_response"] = map[string]any{"type": "create"}
		h.send("PostToolUse", in)
	}
	time.Sleep(200 * time.Millisecond)
	text, errText, ok := hookclient.Query("Command", CommandInput{Name: "summary", Root: h.proj}, 5*time.Second)
	if !ok || errText != "" {
		t.Fatalf("summary: %q %v", errText, ok)
	}
	for _, want := range []string{`요청한 일: "src/auth/session.py 만료 처리 고쳐 줘"`, "지금 하는 일: 최근에 src/theme/dark.css, src/auth/session.py를 바꿨다", "요청 범위 밖으로 보이는 변경: src/theme/dark.css"} {
		if !strings.Contains(text, want) {
			t.Fatalf("summary lacks %q:\n%s", want, text)
		}
	}
}

func TestProtectedPathDenied(t *testing.T) {
	h := startDaemon(t)
	h.declareTestRules("s3.protected_path")
	_ = os.MkdirAll(contract.Dir(h.proj), 0o755)
	_ = os.WriteFile(contract.Path(h.proj), []byte("goal: x\ndone:\n  - check: \"true\"\nscope:\n  protect: [\".env*\"]\n"), 0o644)
	c, raw, _ := contract.Load(h.proj)
	if _, err := contract.Accept(h.proj, c, raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	h.send("UserPromptSubmit", map[string]any{"prompt": "설정 바꿔 줘"})
	out := h.send("PreToolUse", map[string]any{"tool_name": "Write", "tool_use_id": "p1", "tool_input": map[string]any{"file_path": filepath.Join(h.proj, ".env"), "content": "A=1"}})
	hs, _ := out["hookSpecificOutput"].(map[string]any)
	if hs == nil || hs["permissionDecision"] != "deny" {
		t.Fatalf("protected path write must be denied: %v", out)
	}
}

func TestConcurrentHooks(t *testing.T) {
	h := startDaemon(t)
	var wg sync.WaitGroup
	errs := make(chan string, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, _ := json.Marshal(map[string]any{"session_id": fmt.Sprintf("c%d", i%5), "cwd": h.proj, "hook_event_name": "PreToolUse",
				"tool_name": "Read", "tool_use_id": fmt.Sprintf("r%d", i), "tool_input": map[string]any{"file_path": "src/a.py"}})
			conn, err := net.DialTimeout("unix", SocketPath(), time.Second)
			if err != nil {
				errs <- err.Error()
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			req, _ := json.Marshal(map[string]any{"v": 1, "agent": "claude", "event": "PreToolUse", "payload": json.RawMessage(p)})
			_, _ = conn.Write(append(req, '\n'))
			line, err := bufio.NewReader(conn).ReadBytes('\n')
			if err != nil {
				errs <- err.Error()
				return
			}
			var r Response
			if json.Unmarshal(line, &r) != nil || r.Error != "" {
				errs <- string(line)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func TestHookClientPassesWhenDaemonDown(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", base)
	t.Setenv("SAMCHEONPO_HOME", filepath.Join(base, "home"))
	t.Setenv("SAMCHEONPO_NO_SPAWN", "1")
	var out bytes.Buffer
	start := time.Now()
	code := hookclient.Main("PreToolUse", strings.NewReader(`{"session_id":"x","tool_name":"Bash"}`), &out)
	if code != 0 || out.Len() != 0 {
		t.Fatalf("no daemon: exit %d output %q", code, out.String())
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Error("the client must not wait when the daemon is down")
	}
	// a stale socket file with nobody listening also passes
	_ = os.WriteFile(filepath.Join(base, "samcheonpo.sock"), nil, 0o600)
	if code := hookclient.Main("PostToolUse", strings.NewReader(`{"session_id":"x"}`), &out); code != 0 || out.Len() != 0 {
		t.Fatal("stale socket must pass")
	}
	if code := hookclient.Main("PostToolUse", strings.NewReader(`not json`), &out); code != 0 || out.Len() != 0 {
		t.Fatal("invalid input must pass")
	}
}

func TestDaemonKilledMidSession(t *testing.T) {
	h := startDaemon(t)
	h.send("UserPromptSubmit", map[string]any{"prompt": "x"})
	hookclient.Query("Shutdown", map[string]string{}, time.Second)
	<-h.done
	var out bytes.Buffer
	if code := hookclient.Main("PreToolUse", strings.NewReader(`{"session_id":"sess-1","tool_name":"Bash","tool_input":{"command":"ls"}}`), &out); code != 0 || out.Len() != 0 {
		t.Fatal("after the daemon stops, hooks pass through")
	}
	go func() { h.done <- Run(h.db, time.Minute) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, ok := hookclient.Query("Ping", map[string]string{}, time.Second); ok {
			return
		}
		select {
		case err := <-h.done:
			t.Fatalf("daemon restart failed: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("daemon restart timed out")
}

func TestCheckpointTimeoutKillsGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	r := &Runner{Root: dir, Timeout: 300 * time.Millisecond}
	start := time.Now()
	res := r.Run([]contract.Check{{ID: "c1", Check: "sh -c 'echo $$ > " + pidFile + "; sleep 30' & sleep 30"}}, false, nil)
	if !res[0].TimedOut || res[0].Pass || time.Since(start) > 5*time.Second {
		t.Fatalf("timeout result %+v after %v", res[0], time.Since(start))
	}
	time.Sleep(100 * time.Millisecond)
	b, _ := os.ReadFile(pidFile)
	var pid int
	fmt.Sscan(string(b), &pid)
	if pid > 0 && syscall.Kill(pid, 0) == nil {
		t.Errorf("child process %d must be killed with the group", pid)
	}
}

func TestCheckpointBusyAndMid(t *testing.T) {
	r := &Runner{Root: t.TempDir(), Timeout: time.Second, Busy: func() bool { return true }}
	res := r.Run([]contract.Check{{ID: "c1", Check: "true"}}, false, nil)
	if res[0].Skipped == "" {
		t.Fatal("checks wait while the agent's shell runs")
	}
	r.Busy = nil
	res = r.Run([]contract.Check{{ID: "c1", Check: "true"}, {ID: "c2", Check: "true", Pure: true}}, true, nil)
	if res[0].Skipped == "" || res[1].Skipped != "" || !res[1].Pass {
		t.Fatalf("mid-session only runs pure or isolated checks: %+v", res)
	}
	res = r.Run([]contract.Check{{ID: "c1", Check: "true"}}, false, map[string]bool{"c1": true})
	if res[0].Skipped == "" {
		t.Fatal("side-effect checks are skipped")
	}
}

func TestCheckpointSideEffectAndWorktree(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1"), 0o644)
	git(t, dir, "init", "-q")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "i")
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("2"), 0o644)   // uncommitted change
	_ = os.WriteFile(filepath.Join(dir, "new.txt"), []byte("n"), 0o644) // untracked
	ws := NewWorkspace(dir)
	r := &Runner{Root: dir, WS: ws, Timeout: 10 * time.Second}
	res := r.Run([]contract.Check{{ID: "c1", Check: "echo out > made.txt"}}, false, nil)
	if !res[0].SideEffect {
		t.Fatalf("a check that changes the workspace is marked: %+v", res[0])
	}
	_ = os.Remove(filepath.Join(dir, "made.txt"))
	res = r.Run([]contract.Check{{ID: "c2", Isolation: "worktree", Check: "grep -q 2 a.txt && test -f new.txt && echo out > made2.txt"}}, false, nil)
	if !res[0].Pass || res[0].SideEffect {
		t.Fatalf("isolated check sees current changes and leaves the tree alone: %+v", res[0])
	}
	if _, err := os.Stat(filepath.Join(dir, "made2.txt")); !os.IsNotExist(err) {
		t.Fatal("isolated check must not write into the project")
	}
}

func TestWorkspaceFingerprint(t *testing.T) {
	for _, useGit := range []bool{true, false} {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1"), 0o644)
		if useGit {
			git(t, dir, "init", "-q")
			_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.log\n"), 0o644)
		}
		w := NewWorkspace(dir)
		a, err := w.Compute(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		b, _ := w.Compute(t.Context())
		if a != b {
			t.Fatal("unchanged tree must keep its fingerprint")
		}
		if useGit {
			_ = os.WriteFile(filepath.Join(dir, "ignored.log"), []byte("x"), 0o644)
			if c, _ := w.Compute(t.Context()); c != a {
				t.Error("ignored files do not change the git fingerprint")
			}
		}
		_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("2"), 0o644)
		if c, _ := w.Compute(t.Context()); c == a {
			t.Errorf("git=%v: a content change must change the fingerprint", useGit)
		}
	}
}

func TestWatchInvalidates(t *testing.T) {
	dir := t.TempDir()
	w := NewWorkspace(dir)
	if _, err := w.Compute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := w.Watch(100); err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, fresh := w.Current(); !fresh {
		t.Fatal("fresh after compute")
	}
	_ = os.WriteFile(filepath.Join(dir, "user-edit.txt"), []byte("x"), 0o644)
	for i := 0; i < 100; i++ {
		if _, fresh := w.Current(); !fresh {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("a user edit between tool calls must invalidate the fingerprint")
}

func TestPostToolNudgeStuckError(t *testing.T) {
	h := startDaemon(t)
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/a.py 오류 고쳐 줘"})
	var nudge string
	for i := 0; i < 6 && nudge == ""; i++ {
		p := filepath.Join(h.proj, "src", "a.py")
		content := fmt.Sprintf("x = %d\n", i+10)
		_ = os.WriteFile(p, []byte(content), 0o644)
		w := map[string]any{"tool_name": "Write", "tool_use_id": fmt.Sprintf("w%d", i), "tool_input": map[string]any{"file_path": p, "content": content}}
		h.send("PreToolUse", w)
		w["tool_response"] = map[string]any{"type": "update"}
		if c := ctxOf(h.send("PostToolUse", w)); c != "" {
			nudge = c
		}
		time.Sleep(150 * time.Millisecond)
		o := h.shell(fmt.Sprintf("s%d", i), "python3 -m pytest", "Traceback (most recent call last):\n  File \"/w/src/a.py\", line 1, in <module>\nNameError: name 'y' is not defined", 1)
		if c := ctxOf(o); c != "" {
			nudge = c
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !strings.Contains(nudge, "같은 오류가") || !strings.HasPrefix(nudge, "[삼천포] 관찰:") {
		t.Fatalf("stuck error must reach the agent through additionalContext: %q", nudge)
	}
}

func loadFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "hooks", "codex", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func (h *harness) sendAgent(agent, event string, payload map[string]any) map[string]any {
	h.t.Helper()
	conn, err := net.DialTimeout("unix", SocketPath(), time.Second)
	if err != nil {
		h.t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	p, _ := json.Marshal(payload)
	req, _ := json.Marshal(map[string]any{"v": 1, "agent": agent, "event": event, "payload": json.RawMessage(p)})
	_, _ = conn.Write(append(req, '\n'))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		h.t.Fatal(err)
	}
	var resp Response
	_ = json.Unmarshal(line, &resp)
	if resp.Error != "" {
		h.t.Fatalf("%s: %s", event, resp.Error)
	}
	out := map[string]any{}
	if len(resp.Output) > 0 {
		_ = json.Unmarshal(resp.Output, &out)
	}
	return out
}

// TestCodexFixtures replays hook payloads captured from Codex 0.160.0.
func TestCodexFixtures(t *testing.T) {
	h := startDaemon(t)
	_ = os.WriteFile(filepath.Join(h.proj, ".samcheonpo.yml"), []byte("notify: {desktop: false}\nexperiment: {enabled: false}\ncontract: {draft: on}\n"), 0o644)
	rollout := filepath.Join(h.home, "rollout.jsonl")
	_ = os.MkdirAll(h.home, 0o755)
	_ = os.WriteFile(rollout, []byte(`{"type":"turn_context","payload":{"model":"gpt-5.5"}}`+"\n"), 0o644)
	prep := func(name string) map[string]any {
		m := loadFixture(t, name)
		m["cwd"] = h.proj
		m["transcript_path"] = rollout
		m["session_id"] = "codex-1"
		return m
	}
	h.sendAgent("codex", "SessionStart", prep("SessionStart"))
	out := h.sendAgent("codex", "UserPromptSubmit", prep("UserPromptSubmit"))
	if !strings.Contains(ctxOf(out), "contract.yml") {
		t.Fatalf("Codex prompt hook gets the contract draft request: %v", out)
	}
	// apply_patch without a post-tool hook, then a shell call
	patch := prep("PreToolUse-apply_patch")
	h.sendAgent("codex", "PreToolUse", patch)
	pre := prep("PreToolUse-bash")
	post := prep("PostToolUse-bash")
	id := post["tool_use_id"].(string)
	for i := 0; i < 3; i++ {
		pre["tool_use_id"], post["tool_use_id"] = fmt.Sprintf("%s-%d", id, i), fmt.Sprintf("%s-%d", id, i)
		f, _ := os.OpenFile(rollout, os.O_APPEND|os.O_WRONLY, 0o644)
		fmt.Fprintf(f, `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"%s-%d","exit_code":1}}}`+"\n", id, i)
		fmt.Fprintf(f, `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"total_tokens":%d}}}}`+"\n",
			1000*(i+1), 900*(i+1), 50*(i+1), 1050*(i+1))
		f.Close()
		o := h.sendAgent("codex", "PreToolUse", pre)
		if hs, ok := o["hookSpecificOutput"].(map[string]any); ok && hs["permissionDecision"] == "deny" {
			if i != 2 {
				t.Fatalf("only the third identical run is blocked (got %d)", i)
			}
			break
		}
		post["tool_response"] = "FAILED tests/test_calc.py::test_add - assert -1 == 5\n1 failed"
		h.sendAgent("codex", "PostToolUse", post)
		time.Sleep(200 * time.Millisecond)
	}
	stop := prep("Stop")
	stop["last_assistant_message"] = "완료했습니다."
	h.sendAgent("codex", "Stop", stop)
	start := time.Now()
	h.sendAgent("codex", "SessionEnd", prep("SessionEnd"))
	if time.Since(start) > 2*time.Second {
		t.Error("Codex SessionEnd must answer within its 3 second budget")
	}
	time.Sleep(500 * time.Millisecond)
	db, err := ledger.Open(h.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var shells, failed, patches, priced int
	rows, _ := db.Query(`SELECT tool, COALESCE(exit_code,-9), category, cost_micro_krw FROM event WHERE session_id='codex-1'`)
	for rows.Next() {
		var tool, cat string
		var exit int
		var cost int64
		_ = rows.Scan(&tool, &exit, &cat, &cost)
		if tool == "shell" {
			shells++
			if exit == 1 {
				failed++
			}
		}
		if tool == "edit" && exit == -1 && cat == "plan" {
			patches++
		}
		if cost > 0 {
			priced++
		}
	}
	rows.Close()
	if shells < 2 || failed != shells {
		t.Errorf("exit codes come from the rollout: shells=%d failed=%d", shells, failed)
	}
	if patches != 1 {
		t.Errorf("an apply_patch without a result is kept as not executed: %d", patches)
	}
	if priced == 0 {
		t.Error("rollout token records are attributed to events")
	}
	var mode string
	_ = db.QueryRow(`SELECT mode FROM session WHERE id='codex-1'`).Scan(&mode)
	if mode != "live" {
		t.Error("the Codex session is finalized after SessionEnd")
	}
}

func TestFilterOutput(t *testing.T) {
	deny := json.RawMessage(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"x"}}`)
	if FilterOutput("PreToolUse", deny, adapter.ObserveOnly) != nil {
		t.Error("observe-only agents never get a deny")
	}
	if FilterOutput("PreToolUse", deny, adapter.Profiles["codex"].Caps) == nil {
		t.Error("codex supports pre-tool deny")
	}
	end := json.RawMessage(`{"continue":false,"stopReason":"r","decision":"block","reason":"x"}`)
	var m map[string]any
	_ = json.Unmarshal(FilterOutput("PostToolUse", end, adapter.Profiles["codex"].Caps), &m)
	if _, ok := m["continue"]; ok || m["decision"] != "block" {
		t.Errorf("codex cannot end the turn from a post-tool hook: %v", m)
	}
	stop := json.RawMessage(`{"decision":"block","reason":"x","systemMessage":"u"}`)
	if FilterOutput("Stop", stop, adapter.ObserveOnly) != nil {
		t.Error("observe-only agents keep running")
	}
}

func TestCapsResolve(t *testing.T) {
	if c, ok := adapter.Resolve("codex", "codex-cli 0.160.0"); !ok || !c.BlockPre || c.ExitInHook {
		t.Errorf("tested codex version %+v %v", c, ok)
	}
	if _, ok := adapter.Resolve("codex", "codex-cli 0.170.0"); ok {
		t.Error("untested versions fall back to observation")
	}
	if _, ok := adapter.Resolve("claude", "2.1.290 (Claude Code)"); !ok {
		t.Error("tested claude version")
	}
	if _, ok := adapter.Resolve("windsurf", "1.0.0"); ok {
		t.Error("unknown agents observe only")
	}
}

func TestPluginUsageAttribution(t *testing.T) {
	h := startDaemon(t)
	base := map[string]any{"session_id": "oc-1", "cwd": h.proj}
	with := func(m map[string]any) map[string]any {
		for k, v := range base {
			m[k] = v
		}
		return m
	}
	h.sendAgent("opencode", "UserPromptSubmit", with(map[string]any{"prompt": "x"}))
	for _, id := range []string{"c1", "c2"} {
		h.sendAgent("opencode", "PreToolUse", with(map[string]any{"tool_name": "Read", "tool_use_id": id, "tool_input": map[string]any{"file_path": "src/a.py"}}))
		h.sendAgent("opencode", "PostToolUse", with(map[string]any{"tool_name": "Read", "tool_use_id": id, "tool_input": map[string]any{"file_path": "src/a.py"}, "tool_response": map[string]any{"stdout": "x"}}))
	}
	time.Sleep(200 * time.Millisecond)
	usage := func(out int) map[string]any {
		return with(map[string]any{"message_id": "m1", "model": "claude-sonnet-5-5", "usage": map[string]any{"input_tokens": 10, "output_tokens": out, "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}})
	}
	h.sendAgent("opencode", "Usage", usage(100))
	h.sendAgent("opencode", "Usage", usage(300)) // cumulative update of the same message
	db, _ := ledger.Open(h.db)
	defer db.Close()
	var calls, msgs, out int64
	rows, _ := db.Query(`SELECT kind, tokens_out FROM event WHERE session_id='oc-1'`)
	for rows.Next() {
		var k string
		var o int64
		_ = rows.Scan(&k, &o)
		if k == "tool" && o > 0 {
			calls++
		}
		if k == "message" && o > 0 {
			msgs++
		}
		out += o
	}
	rows.Close()
	if calls != 2 || msgs != 0 || out != 300 {
		t.Fatalf("message usage goes to its calls once: calls=%d msgs=%d out=%d", calls, msgs, out)
	}
}

// TestJudgeDrift runs the semantic judge against a local OpenAI-compatible
// fake: two consecutive drift answers raise s3.drift, and the judge's own
// cost is a watch event capped by the configured share.
func TestJudgeDrift(t *testing.T) {
	var calls int
	var mu sync.Mutex
	srv := httptestServer(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"drift"}}],"usage":{"prompt_tokens":50,"completion_tokens":1}}`))
	})
	defer srv.Close()
	h := startDaemon(t)
	cfg := fmt.Sprintf("notify: {desktop: false}\nexperiment: {enabled: false}\ndetectors:\n  s3_drift:\n    judge_every_events: 2\n    max_watch_cost_ratio: 0.5\n    judge: {provider: openai, model: claude-haiku-4-5, base_url: %q}\n", srv.URL+"/v1")
	// The judge connection is user-owned; a project file cannot configure it.
	_ = os.WriteFile(filepath.Join(h.home, "config.yml"), []byte(cfg), 0o644)
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/a.py 고쳐 줘", "session_id": "j-1"})
	if err := os.MkdirAll(contract.Dir(h.proj), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contract.Path(h.proj), []byte("goal: src/a.py 고쳐 줘\ndone:\n  - manual: review output\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, errText, ok := hookclient.Query("Command", CommandInput{Name: "accept", Root: h.proj, Session: "j-1"}, 5*time.Second); !ok || errText != "" {
		t.Fatalf("accept: %s %v", errText, ok)
	}
	// seed spend so the judge has budget
	h.send("Usage", map[string]any{"session_id": "j-1", "message_id": "m0", "model": "claude-opus-5-5",
		"usage": map[string]any{"input_tokens": 100000, "output_tokens": 10000}})
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("r%d", i)
		h.send("PreToolUse", map[string]any{"session_id": "j-1", "tool_name": "Read", "tool_use_id": id, "tool_input": map[string]any{"file_path": "README.md"}})
		h.send("PostToolUse", map[string]any{"session_id": "j-1", "tool_name": "Read", "tool_use_id": id, "tool_input": map[string]any{"file_path": "README.md"}, "tool_response": map[string]any{"stdout": "x"}})
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	db, _ := ledger.Open(h.db)
	defer db.Close()
	var drift, watch int
	_ = db.QueryRow(`SELECT COUNT(*) FROM verdict WHERE session_id='j-1' AND rule='s3.drift'`).Scan(&drift)
	_ = db.QueryRow(`SELECT COUNT(*) FROM event WHERE session_id='j-1' AND category='watch' AND cost_micro_krw > 0`).Scan(&watch)
	mu.Lock()
	defer mu.Unlock()
	if calls < 2 || watch != calls || drift != 1 {
		t.Fatalf("judge calls=%d watch events=%d drift verdicts=%d", calls, watch, drift)
	}
}

// TestIronLaws runs the real iron-laws checker when it is available locally.
func TestIronLaws(t *testing.T) {
	bin := os.Getenv("IRON_LAWS_BIN")
	if bin == "" {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, "jobs", "iron-laws", ".venv", "bin", "iron-laws")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("iron-laws is not installed here")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "a.py")
	_ = os.WriteFile(p, []byte("def f():\n    try:\n        x()\n    except Exception:\n        pass\n"), 0o644)
	fs, err := RunIronLaws(bin, dir, p, 2*time.Minute)
	if err != nil || len(fs) == 0 || fs[0].IronLaw != 3 || fs[0].Line != 4 {
		t.Fatalf("iron-laws findings %+v %v", fs, err)
	}
}

// sendBy sends a hook request carrying an explicit client deadline.
func (h *harness) sendBy(event string, payload map[string]any, deadline time.Time) map[string]any {
	h.t.Helper()
	payload["hook_event_name"] = event
	payload["cwd"] = h.proj
	if _, ok := payload["session_id"]; !ok {
		payload["session_id"] = "sess-1"
	}
	conn, err := net.DialTimeout("unix", SocketPath(), time.Second)
	if err != nil {
		h.t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	p, _ := json.Marshal(payload)
	req, _ := json.Marshal(map[string]any{"v": 1, "agent": "claude", "event": event, "deadline_unix_ms": deadline.UnixMilli(), "payload": json.RawMessage(p)})
	_, _ = conn.Write(append(req, '\n'))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		h.t.Fatal(err)
	}
	var resp Response
	_ = json.Unmarshal(line, &resp)
	out := map[string]any{}
	if len(resp.Output) > 0 {
		_ = json.Unmarshal(resp.Output, &out)
	}
	return out
}

func TestBlockDecidedAfterClientDeadlineIsReportedUndelivered(t *testing.T) {
	h := startDaemon(t)
	h.declareTestRules("s1.identical_rerun")
	h.send("UserPromptSubmit", map[string]any{"prompt": "src/a.py 시험 통과시켜 줘"})
	for i := 0; i < 2; i++ {
		h.shell(fmt.Sprintf("t%d", i), "pytest -q", "FAILED tests/test_a.py::test_x - assert 1 == 2\n1 failed", 1)
		time.Sleep(150 * time.Millisecond)
	}
	status := func() string {
		text, _, _ := hookclient.Query("Statusline", map[string]any{"session_id": "sess-1"}, 2*time.Second)
		return text
	}
	if strings.Contains(status(), "판정 시간 초과") {
		t.Fatal("no late decision yet")
	}
	in := map[string]any{"tool_name": "Bash", "tool_use_id": "t2", "tool_input": map[string]any{"command": "pytest -q"}}
	out := h.sendBy("PreToolUse", in, time.Now().Add(-time.Second))
	hs, _ := out["hookSpecificOutput"].(map[string]any)
	if hs == nil || hs["permissionDecision"] != "deny" {
		t.Fatalf("the rule still decides to block: %v", out)
	}
	if !strings.Contains(status(), "판정 시간 초과 1건") {
		t.Fatalf("a block decided after the client stopped waiting is reported as possibly undelivered: %q", status())
	}
}
