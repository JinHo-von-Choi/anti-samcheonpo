package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/learning"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// All labels in this fixture are synthetic; no product accuracy is claimed.
func TestLearningCLIRecomputesEvaluationInstallsShadowAndLoadsRule(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAMCHEONPO_HOME", home)
	dir := t.TempDir()
	labelsDir := filepath.Join(dir, "labels")
	split := eval.Split{Version: "split/2", Purpose: "preregistered_holdout", Calibration: []string{"train"}, Groups: map[string]eval.SplitGroup{"train": {Repository: "train-r", Family: "train-f"}}}
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	prices, _ := cost.Load("")
	cfg := config.Default()
	ids := []string{"train"}
	for i := 0; i < 160; i++ {
		id := fmt.Sprintf("held-%03d", i)
		ids = append(ids, id)
		split.Holdout = append(split.Holdout, id)
		split.Groups[id] = eval.SplitGroup{Repository: "repo-" + id, Family: "family-" + id}
	}
	for i, id := range ids {
		positive := i <= 80
		s := &event.Session{ID: id, Agent: "claude", Mode: "audit", FirstPrompt: "자료 조사", StartedAt: time.Now()}
		for seq := int64(0); seq < 3; seq++ {
			content := "same content"
			if !positive {
				content = fmt.Sprint(seq)
			}
			s.Events = append(s.Events, &event.Event{SessionID: id, Seq: seq, Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{"paper"}, Text: content, Priced: true})
		}
		r, err := analyze.Run(s, analyze.Options{Config: cfg, Prices: prices})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.SaveAnalysis(r, 0, 0, nil); err != nil {
			t.Fatal(err)
		}
		symptom := "none"
		if positive {
			symptom = "S4"
		}
		people := []string{"independent-a", "independent-b"}
		if id == "train" {
			people = []string{"trainer"}
		}
		for _, person := range people {
			if err = os.MkdirAll(filepath.Join(labelsDir, person), 0700); err != nil {
				t.Fatal(err)
			}
			l := eval.Label{Session: id, Start: 0, End: 2, Symptom: symptom, Justified: "yes"}
			b, _ := json.Marshal(l)
			if err = os.WriteFile(filepath.Join(labelsDir, person, id+".jsonl"), append(b, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	db.Close()
	splitPath := filepath.Join(dir, "split.json")
	b, _ := json.Marshal(split)
	if err = os.WriteFile(splitPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	pPath := filepath.Join(dir, "proposal.json")
	commands := learningCmds()
	commands[0].SetArgs([]string{"--labels", labelsDir, "--split", splitPath, "--out", pPath})
	if err = commands[0].Execute(); err != nil {
		t.Fatal(err)
	}
	proposal, err := loadProposal(pPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposal.Definitions) != 1 {
		t.Fatal(proposal)
	}
	var report bytes.Buffer
	commands[1].SetOut(&report)
	commands[1].SetArgs([]string{pPath, "--labels", labelsDir, "--split", splitPath, "--check"})
	if err = commands[1].Execute(); err != nil {
		t.Fatal(err)
	}
	var evaluation learning.Evaluation
	if err = json.Unmarshal(report.Bytes(), &evaluation); err != nil || !evaluation.Passed {
		t.Fatalf("%s %v", &report, err)
	}
	commands[2].SetOut(new(bytes.Buffer))
	commands[2].SetArgs([]string{pPath, "--labels", labelsDir, "--split", splitPath, "--install"})
	if err = commands[2].Execute(); err != nil {
		t.Fatal(err)
	}
	pack, err := rulepack.Load(filepath.Join(home, "learned-rules.json"))
	if err != nil || len(pack.Definitions) != 1 {
		t.Fatalf("%+v %v", pack, err)
	}
	if _, err = os.Stat(filepath.Join(config.Home(), "config.yml")); !os.IsNotExist(err) {
		t.Fatal("promotion changed user settings")
	}
	// Feed native Claude transcript input through the real audit entry point.
	source := filepath.Join(dir, "trace.jsonl")
	var trace bytes.Buffer
	rows := []map[string]any{{"type": "user", "sessionId": "synthetic", "message": map[string]any{"role": "user", "content": "근거 조사"}}}
	for i := 0; i < 3; i++ {
		call := fmt.Sprint(i)
		rows = append(rows, map[string]any{"type": "assistant", "sessionId": "synthetic", "message": map[string]any{"role": "assistant", "id": "a" + call, "model": "claude-sonnet-4-5", "content": []any{map[string]any{"type": "tool_use", "id": call, "name": "Read", "input": map[string]any{"file_path": "/fixture/paper.txt"}}}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}}}, map[string]any{"type": "user", "sessionId": "synthetic", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": call, "content": "same content"}}}})
	}
	for _, row := range rows {
		b, _ := json.Marshal(row)
		trace.Write(b)
		trace.WriteByte('\n')
	}
	if err = os.WriteFile(source, trace.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := analyzeFile(sources.File{Path: source, Agent: "claude"}, cfg, config.Hash(cfg), prices)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range result.Verdicts {
		if v.Rule == pack.Definitions[0].RuleID() {
			found = true
			if !v.Estimate || v.Level != 1 || v.WasteMicro != 0 {
				t.Fatal(v)
			}
		}
	}
	if !found {
		t.Fatal("installed rule was not loaded by native replay")
	}
	if pack.Effectiveness != "unvalidated" {
		t.Fatal("classifier validation became a cost-savings claim")
	}
	// A forged report file has no CLI input path: promote recomputes from labels.
	lFile := filepath.Join(labelsDir, "independent-b", split.Holdout[0]+".jsonl")
	l := eval.Label{Session: split.Holdout[0], Start: 0, End: 2, Symptom: "none"}
	b, _ = json.Marshal(l)
	os.WriteFile(lFile, append(b, '\n'), 0600)
	failed := learningCmds()[2]
	failed.SetOut(new(bytes.Buffer))
	failed.SetErr(new(bytes.Buffer))
	failed.SetArgs([]string{pPath, "--labels", labelsDir, "--split", splitPath})
	if err = failed.Execute(); err == nil {
		t.Fatal("disputed independent labels promoted")
	}
}
