package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// Uses only the standard library and static golden data, never the candidate's
// seal implementation. Mutations are submitted through the real CLI boundary.
func checkTaskSpec(impl, dir string) (int, int) {
	input, err := os.ReadFile(filepath.Join(dir, "task-input.json"))
	if err != nil {
		fmt.Println("FAIL v2 fixture", err)
		return 1, 1
	}
	golden, err := os.ReadFile(filepath.Join(dir, "task-expected.json"))
	if err != nil {
		fmt.Println("FAIL v2 golden", err)
		return 1, 1
	}
	var expected any
	if json.Unmarshal(golden, &expected) != nil {
		fmt.Println("FAIL v2 golden JSON")
		return 1, 1
	}
	work, err := os.MkdirTemp("", "samcheonpo-conformance-v2-")
	if err != nil {
		fmt.Println("FAIL v2 temp", err)
		return 1, 1
	}
	defer os.RemoveAll(work)
	parts := strings.Fields(impl)
	if len(parts) == 0 {
		return 1, 1
	}
	cases := []struct {
		name   string
		reject bool
		change func(map[string]any)
	}{
		{"canonical", false, func(map[string]any) {}},
		{"verify-seal", false, func(m map[string]any) { m["seal"] = expected }},
		{"future-version", true, func(m map[string]any) { m["spec"] = "evidence-ledger/999" }},
		{"tool-origin", true, func(m map[string]any) {
			m["revisions"].([]any)[0].(map[string]any)["source"].(map[string]any)["origin"] = "tool"
		}},
		{"revision-gap", true, func(m map[string]any) {
			r := m["revisions"].([]any)[0].(map[string]any)
			r["number"] = 3
			r["parent"] = 2
		}},
		{"missing-input", true, func(m map[string]any) {
			m["evidence"].([]any)[0].(map[string]any)["key"].(map[string]any)["input_hash"] = ""
		}},
		{"duplicate-proof", true, func(m map[string]any) { p := m["evidence"].([]any); m["evidence"] = append(p, p[0]) }},
		{"false-pass", true, func(m map[string]any) { m["evidence"].([]any)[0].(map[string]any)["exit_code"] = 1 }},
		{"tampered-goal", true, func(m map[string]any) {
			m["seal"] = expected
			m["revisions"].([]any)[0].(map[string]any)["goal"] = "changed"
		}},
		{"unknown-field", true, func(m map[string]any) { m["approval"] = true }},
	}
	failed := 0
	for _, tc := range cases {
		var m map[string]any
		_ = json.Unmarshal(input, &m)
		tc.change(m)
		b, _ := json.Marshal(m)
		path := filepath.Join(work, tc.name+".json")
		if err := os.WriteFile(path, b, 0600); err != nil {
			failed++
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, parts[0], append(parts[1:], path)...)
		cmd.WaitDelay = time.Second
		out, err := cmd.Output()
		cancel()
		pass := false
		if tc.reject {
			if e, ok := err.(*exec.ExitError); ok && e.ExitCode() > 0 {
				pass = true
			}
		} else if err == nil {
			var got any
			pass = json.Unmarshal(out, &got) == nil && reflect.DeepEqual(got, expected)
		}
		if pass {
			fmt.Println("PASS v2", tc.name)
		} else {
			failed++
			fmt.Println("FAIL v2", tc.name)
		}
	}
	return len(cases), failed
}
