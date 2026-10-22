package detect

import (
	"slices"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
)

func TestShortCircuitFindsAConstantReturnThatIgnoresParameters(t *testing.T) {
	d := NewShortCircuit()
	added := []string{
		"func IsValidUser(id string) bool {",
		"\treturn true",
		"}",
	}
	v := d.InspectAdded("auth.go", added)
	if !v.Found {
		t.Fatal("a function that returns a constant without reading its parameters is found")
	}
	if v.Func != "IsValidUser" {
		t.Fatalf("the function is named: %q", v.Func)
	}
}

func TestShortCircuitAcceptsAFunctionThatReadsAParameter(t *testing.T) {
	d := NewShortCircuit()
	added := []string{
		"func IsValidUser(id string) bool {",
		"\treturn id != \"\"",
		"}",
	}
	if v := d.InspectAdded("auth.go", added); v.Found {
		t.Fatalf("a function that reads its parameter is not a finding: %+v", v)
	}
}

func TestShortCircuitAcceptsANonConstantReturn(t *testing.T) {
	d := NewShortCircuit()
	added := []string{
		"func IsValidUser(id string) bool {",
		"\treturn lookup(id)",
		"}",
	}
	if v := d.InspectAdded("auth.go", added); v.Found {
		t.Fatalf("a function that computes its answer is not a finding: %+v", v)
	}
}

func TestShortCircuitReadsPythonAndJavaScriptShapes(t *testing.T) {
	d := NewShortCircuit()
	py := []string{"def is_valid_user(user_id):", "    return True"}
	if v := d.InspectAdded("auth.py", py); !v.Found {
		t.Fatal("a python function that returns a constant is found")
	}
	js := []string{"function isValidUser(userId) {", "  return true;"}
	if v := d.InspectAdded("auth.js", js); !v.Found {
		t.Fatal("a javascript function that returns a constant is found")
	}
	jsUsed := []string{"function isValidUser(userId) {", "  return userId != null;"}
	if v := d.InspectAdded("auth.js", jsUsed); v.Found {
		t.Fatalf("a javascript function that reads its parameter is not a finding: %+v", v)
	}
}

func TestShortCircuitIgnoresAFunctionWithoutParameters(t *testing.T) {
	d := NewShortCircuit()
	added := []string{"func Version() string {", "\treturn \"1.0\"", "}"}
	if v := d.InspectAdded("version.go", added); v.Found {
		t.Fatalf("a function without parameters is not a finding: %+v", v)
	}
}

// The finding is recorded. It never stops the write, and the rule starts in
// shadow.
func TestShortCircuitIsRecordedAndNeverBlocks(t *testing.T) {
	b := newB(t, "auth.go 고쳐 줘", nil)
	sigs := b.write("auth.go", "hashA", []string{
		"func IsValidUser(id string) bool {",
		"\treturn true",
		"}",
	}, nil)
	found := false
	for _, sig := range sigs {
		if sig.Rule == "s5.short_circuit" {
			found = true
			if sig.Facts["blocked"] == true {
				t.Fatal("the rule is recorded, not a block")
			}
			if sig.Facts["func"] != "IsValidUser" {
				t.Fatalf("the finding names the function: %+v", sig.Facts)
			}
		}
	}
	if !found {
		t.Fatal("the short circuit is recorded")
	}
	if !slices.Contains(config.Default().Rollout.ShadowRules, "s5.short_circuit") {
		t.Fatal("the rule starts in shadow")
	}
}

// A test file is out of scope for the rule: a test that returns a constant
// is the assertion guard's business, not this rule's.
func TestShortCircuitSkipsTestPaths(t *testing.T) {
	b := newB(t, "auth_test.go 고쳐 줘", nil)
	sigs := b.write("auth_test.go", "hashA", []string{
		"func TestIsValidUser(t *testing.T) {",
		"\treturn true",
		"}",
	}, nil)
	for _, sig := range sigs {
		if sig.Rule == "s5.short_circuit" {
			t.Fatal("a test file is out of scope for this rule")
		}
	}
}
