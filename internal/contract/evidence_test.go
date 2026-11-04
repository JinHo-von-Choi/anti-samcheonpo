package contract

import (
	"strings"
	"testing"
)

func TestTypedAcceptanceDeclaration(t *testing.T) {
	for _, kind := range []string{"document", "ui", "data", "api", "deployment"} {
		raw := "spec: progress-contract/2\ngoal: acceptance\ndone:\n - id: result\n   check: test -s result.json\n   evidence:\n     kind: " + kind + "\n     artifacts: [result.json]\n"
		c, errs := Parse([]byte(raw))
		if len(errs) != 0 || c.Done[0].Evidence.Kind != kind {
			t.Fatalf("%s: %v", kind, errs)
		}
		for _, bad := range []string{"../outside", "/tmp/outside", "C:/outside", "C:outside", "//server/share/file", "*.png"} {
			if _, errs := Parse([]byte(strings.Replace(raw, "[result.json]", "["+bad+"]", 1))); len(errs) == 0 {
				t.Fatalf("accepted invalid artifact %s", bad)
			}
		}
		if _, errs := Parse([]byte(strings.Replace(raw, "[result.json]", "[]", 1))); len(errs) == 0 {
			t.Fatal("typed acceptance without artifacts")
		}
		if _, errs := Parse([]byte(strings.Replace(raw, "progress-contract/2", "progress-contract/1", 1))); len(errs) == 0 {
			t.Fatal("v1 silently gained authority")
		}
	}
}
