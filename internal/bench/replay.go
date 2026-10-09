package bench

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// ReplayResult describes one attempt to turn a span of a real session into a
// bench/live task. A span that cannot be restored exactly is reported with
// its reason and no task is written; it is counted, not guessed.
type ReplayResult struct {
	Session     string `json:"session"`
	At          int64  `json:"at"`
	Task        string `json:"task,omitempty"`
	Dir         string `json:"dir,omitempty"`
	BaseCommit  string `json:"base_commit,omitempty"`
	Replayed    int    `json:"replayed_writes"`
	Verified    int    `json:"verified_writes"`
	Check       string `json:"check,omitempty"`
	Recoverable bool   `json:"recoverable"`
	Reason      string `json:"reason,omitempty"`
}

// BuildReplay restores the repository of a Claude Code session as it was
// just before event at: the last commit before the session started, plus the
// session's own Write/Edit inputs before at, each checked against the content
// hash the transcript recorded. The check is the first verification that
// passed after at. The task carries the session as its group and has no
// grader directory, so it stays a non-independent task until reviewed.
func BuildReplay(s *event.Session, transcript string, at int64, out string) (ReplayResult, error) {
	r := ReplayResult{Session: s.ID, At: at}
	fail := func(reason string) (ReplayResult, error) {
		r.Reason = reason
		return r, nil
	}
	if s.Agent != "claude" {
		return fail("Claude Code 기록만 복원할 수 있다")
	}
	root := s.ProjectPath
	if out, err := exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree").Output(); err != nil || strings.TrimSpace(string(out)) != "true" {
		return fail("원본 프로젝트가 git 저장소로 남아 있지 않다")
	}
	base, err := exec.Command("git", "-C", root, "rev-list", "-1", "--before="+s.StartedAt.UTC().Format(time.RFC3339), "HEAD").Output()
	if err != nil || strings.TrimSpace(string(base)) == "" {
		return fail("세션 시작 전 커밋을 찾지 못했다")
	}
	r.BaseCommit = strings.TrimSpace(string(base))
	var writes []*event.Event
	var prompt string
	for _, ev := range s.Events {
		if ev.Kind == event.KindTool && ev.Category == "" {
			classify.Initial(ev, classify.Options{})
		}
	}
	for _, ev := range s.Events {
		if ev.Seq >= at {
			if r.Check == "" && detect.ExecutedVerify(ev) && ev.ExitCode != nil && *ev.ExitCode == 0 {
				r.Check = ev.Cmd
			}
			continue
		}
		if ev.Kind == event.KindPrompt && !ev.Forced && strings.TrimSpace(ev.Text) != "" {
			prompt = ev.Text
		}
		if ev.Kind != event.KindTool {
			continue
		}
		if ev.Tool == event.ToolShell && (ev.Mutating || ev.Unknown) {
			return fail("분기점 전에 쉘 명령이 파일을 바꿔 정확히 복원할 수 없다")
		}
		if ev.Category == event.CatProduce && (ev.Tool == event.ToolWrite || ev.Tool == event.ToolEdit) {
			writes = append(writes, ev)
		}
	}
	if r.Check == "" {
		return fail("분기점 이후 통과한 검증이 없어 채점 명령을 정할 수 없다")
	}
	if prompt == "" {
		prompt = s.FirstPrompt
	}
	inputs, err := toolInputs(transcript, writes)
	if err != nil {
		return r, err
	}
	name := fmt.Sprintf("replay-%s-%d", short8(s.ID), at)
	dir := filepath.Join(out, name)
	repo := filepath.Join(dir, "repo")
	if _, err := os.Stat(dir); err == nil {
		return r, fmt.Errorf("%s가 이미 있다", dir)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return r, err
	}
	cleanup := func(reason string) (ReplayResult, error) {
		os.RemoveAll(dir)
		return fail(reason)
	}
	archive := exec.Command("git", "-C", root, "archive", r.BaseCommit)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return r, err
	}
	if err := archive.Start(); err != nil {
		return r, err
	}
	extractErr := extractTar(pipe, repo)
	// the pipe must be drained before Wait, or git blocks on a full pipe
	_, _ = io.Copy(io.Discard, pipe)
	if err := archive.Wait(); err != nil || extractErr != nil {
		return cleanup("기준 커밋을 풀지 못했다")
	}
	for _, ev := range writes {
		in, ok := inputs[ev.CallID]
		if !ok {
			return cleanup("기록에서 쓰기 입력을 찾지 못했다")
		}
		rel, content, ok := applyInput(root, repo, in)
		if !ok {
			return cleanup("쓰기를 기준 커밋 위에 그대로 적용할 수 없다(세션 전 미커밋 변경 가능)")
		}
		r.Replayed++
		if want := ev.WriteHashes[rel]; want != "" && !strings.HasPrefix(want, "edit:") {
			if contentHash(content) != want {
				return cleanup("복원한 파일 지문이 기록과 다르다: " + rel)
			}
			r.Verified++
		}
	}
	if r.Verified == 0 {
		return cleanup("기록과 대조할 수 있는 쓰기가 없어 복원을 확인하지 못했다")
	}
	task := map[string]any{
		"name": name, "family": "replay", "group": s.ID, "initially_correct": false, "timeout_sec": 900,
		"prompt": prompt, "check": `cd "$BENCH_REPO" && ` + r.Check,
	}
	b, _ := yaml.Marshal(task)
	if err := os.WriteFile(filepath.Join(dir, "task.yml"), b, 0o600); err != nil {
		return r, err
	}
	r.Task, r.Dir, r.Recoverable = name, dir, true
	return r, nil
}

// toolInput is one Write/Edit/MultiEdit call as the transcript recorded it.
type toolInput struct {
	Name  string
	Input map[string]any
}

// toolInputs reads the exact inputs of the given tool calls from a Claude
// Code transcript.
func toolInputs(path string, evs []*event.Event) (map[string]toolInput, error) {
	want := map[string]bool{}
	for _, ev := range evs {
		want[ev.CallID] = true
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]toolInput{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		if !bytes.Contains(sc.Bytes(), []byte(`"tool_use"`)) {
			continue
		}
		var line struct {
			Message struct {
				Content []struct {
					Type  string         `json:"type"`
					ID    string         `json:"id"`
					Name  string         `json:"name"`
					Input map[string]any `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		for _, c := range line.Message.Content {
			if c.Type == "tool_use" && want[c.ID] {
				out[c.ID] = toolInput{c.Name, c.Input}
			}
		}
	}
	return out, sc.Err()
}

// applyInput applies one recorded write to the restored repository and
// returns the project-relative path and the resulting content.
func applyInput(root, repo string, in toolInput) (string, string, bool) {
	p, _ := in.Input["file_path"].(string)
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", "", false
	}
	target := filepath.Join(repo, rel)
	var content string
	switch in.Name {
	case "Write":
		content, _ = in.Input["content"].(string)
	case "Edit", "MultiEdit":
		b, err := os.ReadFile(target)
		if err != nil {
			return "", "", false
		}
		content = string(b)
		edits := []map[string]any{in.Input}
		if in.Name == "MultiEdit" {
			edits = nil
			list, _ := in.Input["edits"].([]any)
			for _, e := range list {
				if m, ok := e.(map[string]any); ok {
					edits = append(edits, m)
				}
			}
		}
		for _, e := range edits {
			old, _ := e["old_string"].(string)
			repl, _ := e["new_string"].(string)
			all, _ := e["replace_all"].(bool)
			if old == "" || !strings.Contains(content, old) {
				return "", "", false
			}
			if all {
				content = strings.ReplaceAll(content, old, repl)
			} else {
				content = strings.Replace(content, old, repl, 1)
			}
		}
	default:
		return "", "", false
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", "", false
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return "", "", false
	}
	return filepath.ToSlash(rel), content, true
}

// contentHash matches the Claude parser's file content hash.
func contentHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16])
}

func short8(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
