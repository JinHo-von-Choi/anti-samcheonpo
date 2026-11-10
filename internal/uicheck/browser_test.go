//go:build browser

package uicheck

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const page = `<!doctype html><title>t</title><h1>Orders</h1>
<div id=log></div>
<button id=ok onclick="document.getElementById('log').textContent='saved'">Save</button>
<button>Export</button>
<a href="#">Details</a>
<a href="other.html">Next</a>
<a href="https://example.com/" >Docs</a>
<button onclick="throw 1">Delete everything</button>`

func TestCheckPressesTargetsInARealBrowser(t *testing.T) {
	browser := FindBrowser()
	if browser == "" {
		t.Fatal("the browser tag needs a Chromium-family browser")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.html"), []byte("<h1>other</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	r, err := Check(ctx, browser, "file://"+filepath.ToSlash(filepath.Join(dir, "index.html")))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"Save": EffectChanged, "Export": EffectNone, "Details": EffectNone, "Docs": EffectLink, "Delete everything": EffectSkipped}
	got := map[string]string{}
	for _, x := range r.Targets {
		got[x.Label] = x.Effect
	}
	for label, effect := range want {
		if got[label] != effect {
			t.Errorf("%s: got %q, want %q (all: %v)", label, got[label], effect, got)
		}
	}
	if got["Next"] == EffectNone || got["Next"] == EffectError {
		t.Errorf("a working link has an effect: %q", got["Next"])
	}
	if len(r.Order) == 0 || r.Order[0] != "h1: Orders" {
		t.Errorf("order: %v", r.Order)
	}
}
