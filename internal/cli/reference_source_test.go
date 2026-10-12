package cli

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeReferenceRequiresFrozenBytesAndMatchingEventOrder(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "native.jsonl")
	raw, err := os.ReadFile(filepath.Join("..", "..", "conformance", "cases", "claude-requirement-real-to-mock", "input.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := sources.Parse(sources.File{Path: source, Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	prices, _ := cost.Load("")
	res, err := analyze.Run(s, analyze.Options{Config: config.Default(), Prices: prices})
	if err != nil {
		t.Fatal(err)
	}
	db, err := ledger.Open(filepath.Join(root, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.SaveAnalysis(res, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	recorded, err := db.EventsOf(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := eval.NativeSourceDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	item := eval.ReviewItem{Session: s.ID, Agent: "claude", NativeSourceSHA256: sha}
	if _, why := nativeReferenceEvents(db, item, recorded); why != "native_frozen_bytes_verified" {
		t.Fatal(why)
	}
	recorded[0].Seq++
	if _, why := nativeReferenceEvents(db, item, recorded); why != "native_sequence_mismatch" {
		t.Fatal("mismatched event attribution accepted", why)
	}
	recorded[0].Seq--
	if err = os.WriteFile(source, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, why := nativeReferenceEvents(db, item, recorded); why != "native_source_changed" {
		t.Fatal("changed native bytes accepted", why)
	}
}
