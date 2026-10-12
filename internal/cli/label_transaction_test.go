package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
)

func TestLabelSaveRejectsStaleReviewAndCrossFileDuplicate(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "reviewer")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "session.jsonl")
	meta := &eval.Meta{Success: "unknown"}
	first := eval.Label{Session: "s", Start: 1, End: 2, Symptom: "S4"}
	if err := saveLabels(out, "s", "reviewer", []eval.Label{first}, meta, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(out)
	second := eval.Label{Session: "s", Start: 3, End: 4, Symptom: "none"}
	if err := saveLabels(out, "s", "reviewer", []eval.Label{second}, meta, nil, io.Discard); err == nil {
		t.Fatal("stale interactive review overwrote a newer answer")
	}
	after, _ := os.ReadFile(out)
	if !bytes.Equal(before, after) {
		t.Fatal("stale write changed answers")
	}
	other := filepath.Join(dir, "web.jsonl")
	if err := saveLabels(other, "s", "reviewer", []eval.Label{first}, meta, nil, io.Discard); err == nil {
		t.Fatal("same reviewer duplicated a vote through another file")
	}
	if err := saveLabels(out, "s", "reviewer", []eval.Label{first, second}, meta, before, io.Discard); err != nil {
		t.Fatal(err)
	}
	labels, err := eval.LoadLabels(root)
	if err != nil || len(labels) != 3 {
		t.Fatalf("lost saved review: %v %v", labels, err)
	}
}
