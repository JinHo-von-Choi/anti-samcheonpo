// Command samcheonpo-conformance checks an implementation of Progress
// Contract v1 and Evidence Ledger v1 against conformance/cases. It uses only
// the standard library and talks to the implementation through a command:
//
//	samcheonpo-conformance --impl "samcheonpo spec run" --cases conformance/cases
//
// The implementation is run as `<impl> <input> [--agent A] [--contract FILE]`
// and must print the JSON described in docs/spec.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type caseFile struct {
	Input    string `json:"input"`
	Agent    string `json:"agent"`
	Contract string `json:"contract"`
	Expect   expect `json:"expect"`
	Same     string `json:"same_verdicts_as"`
}

type expect struct {
	Rules         []string         `json:"rules"` // "rule@L1" (level at least) or "rule" (any level)
	NotRules      []string         `json:"not_rules"`
	ContractState string           `json:"contract_state"`
	Grade         string           `json:"grade"`
	BucketTokens  map[string]int64 `json:"bucket_tokens"`
	TotalTokens   int64            `json:"total_tokens"`
}

type output struct {
	Spec          string `json:"spec"`
	ContractSpec  string `json:"contract_spec"`
	ContractState string `json:"contract_state"`
	Grade         string `json:"grade"`
	Seal          struct {
		Head        string           `json:"head"`
		Rows        int              `json:"rows"`
		TotalMicro  int64            `json:"total_micro_krw"`
		TotalTokens int64            `json:"total_tokens"`
		BucketMicro map[string]int64 `json:"bucket_micro_krw"`
	} `json:"seal"`
	Rows []struct {
		Kind  string `json:"kind"`
		Data  string `json:"data"`
		Chain string `json:"chain"`
	} `json:"rows"`
	Verdicts []struct {
		Seq   int64  `json:"seq"`
		Rule  string `json:"rule"`
		Level int    `json:"level"`
	} `json:"verdicts"`
	BucketTokens map[string]int64 `json:"bucket_tokens"`
}

func link(prev, data string) string {
	h := sha256.New()
	h.Write([]byte(prev))
	h.Write([]byte{0})
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func run(impl, dir string, c caseFile) (output, error) {
	var o output
	parts := strings.Fields(impl)
	args := append(parts[1:], filepath.Join(dir, c.Input))
	if c.Agent != "" {
		args = append(args, "--agent", c.Agent)
	}
	if c.Contract != "" {
		args = append(args, "--contract", filepath.Join(dir, c.Contract))
	}
	b, err := exec.Command(parts[0], args...).Output()
	if err != nil {
		return o, fmt.Errorf("implementation failed: %w", err)
	}
	return o, json.Unmarshal(b, &o)
}

func check(o output, c caseFile) []string {
	var bad []string
	if o.Spec != "evidence-ledger/1" || o.ContractSpec != "progress-contract/1" {
		bad = append(bad, "spec identifiers")
	}
	prev := ""
	buckets := map[string]int64{}
	var tokens int64
	for i, r := range o.Rows {
		prev = link(prev, r.Data)
		if prev != r.Chain {
			bad = append(bad, fmt.Sprintf("chain broken at row %d", i))
			break
		}
		if r.Kind == "event" {
			var m map[string]any
			if err := json.Unmarshal([]byte(r.Data), &m); err != nil {
				bad = append(bad, fmt.Sprintf("row %d is not JSON", i))
				continue
			}
			bucket, _ := m["bucket"].(string)
			for _, k := range []string{"in", "out", "cache_read", "cache_write"} {
				v, _ := m[k].(float64)
				tokens += int64(v)
				buckets[bucket] += int64(v)
			}
		}
	}
	if len(o.Rows) > 0 && prev != o.Seal.Head {
		bad = append(bad, "head does not match the last chain value")
	}
	if len(o.Rows) != o.Seal.Rows {
		bad = append(bad, "row count differs from the seal")
	}
	if tokens != o.Seal.TotalTokens {
		bad = append(bad, fmt.Sprintf("row tokens %d != seal %d", tokens, o.Seal.TotalTokens))
	}
	got := map[string]int{}
	for _, v := range o.Verdicts {
		if l, ok := got[v.Rule]; !ok || v.Level > l {
			got[v.Rule] = v.Level
		}
	}
	for _, r := range c.Expect.Rules {
		rule, lvl, hasLvl := strings.Cut(r, "@L")
		l, ok := got[rule]
		if !ok || (hasLvl && fmt.Sprint(l) < lvl) {
			bad = append(bad, "missing verdict "+r)
		}
	}
	for _, r := range c.Expect.NotRules {
		if _, ok := got[r]; ok {
			bad = append(bad, "unexpected verdict "+r)
		}
	}
	if c.Expect.ContractState != "" && o.ContractState != c.Expect.ContractState {
		bad = append(bad, "contract state "+o.ContractState)
	}
	if c.Expect.Grade != "" && o.Grade != c.Expect.Grade {
		bad = append(bad, "grade "+o.Grade)
	}
	if c.Expect.TotalTokens != 0 && c.Expect.TotalTokens != o.Seal.TotalTokens {
		bad = append(bad, fmt.Sprintf("total tokens %d, want %d", o.Seal.TotalTokens, c.Expect.TotalTokens))
	}
	for k, v := range c.Expect.BucketTokens {
		if buckets[k] != v {
			bad = append(bad, fmt.Sprintf("bucket %s tokens %d, want %d", k, buckets[k], v))
		}
	}
	return bad
}

func ruleSet(o output) string {
	var s []string
	for _, v := range o.Verdicts {
		s = append(s, fmt.Sprintf("%s@%d", v.Rule, v.Level))
	}
	sort.Strings(s)
	return strings.Join(s, ",")
}

func main() {
	impl := flag.String("impl", "samcheonpo spec run", "implementation command")
	dir := flag.String("cases", "conformance/cases", "case directory")
	taskImpl := flag.String("task-impl", "", "optional v2 interface command, e.g. samcheonpo spec task")
	taskDir := flag.String("task-cases", "conformance/v2", "v2 static conformance fixtures")
	flag.Parse()
	files, _ := filepath.Glob(filepath.Join(*dir, "*", "case.json"))
	sort.Strings(files)
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "no cases found")
		os.Exit(2)
	}
	outs := map[string]output{}
	fail := 0
	for _, f := range files {
		name := filepath.Base(filepath.Dir(f))
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Println("FAIL", name, err)
			fail++
			continue
		}
		var c caseFile
		if err := json.Unmarshal(b, &c); err != nil {
			fmt.Println("FAIL", name, err)
			fail++
			continue
		}
		o, err := run(*impl, filepath.Dir(f), c)
		if err != nil {
			fmt.Println("FAIL", name, err)
			fail++
			continue
		}
		outs[name] = o
		bad := check(o, c)
		if c.Same != "" {
			if other, ok := outs[c.Same]; !ok || ruleSet(other) != ruleSet(o) || other.ContractState != o.ContractState {
				bad = append(bad, "verdicts differ from "+c.Same)
			}
		}
		if len(bad) > 0 {
			fail++
			fmt.Println("FAIL", name, strings.Join(bad, "; "))
		} else {
			fmt.Println("PASS", name)
		}
	}
	total := len(files)
	if *taskImpl != "" {
		n, bad := checkTaskSpec(*taskImpl, *taskDir)
		total += n
		fail += bad
	}
	fmt.Printf("%d/%d cases passed\n", total-fail, total)
	if fail > 0 {
		os.Exit(1)
	}
}
