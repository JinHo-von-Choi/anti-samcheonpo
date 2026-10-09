package notify

import (
	"strings"
	"testing"
)

func TestToastScriptKeepsTextInsideItsLiterals(t *testing.T) {
	script := toastScript("it's done", "a 'b' $(calc) `x`\nline 2")
	for _, want := range []string{`'it''s done'`, `'a ''b'' $(calc) ` + "`x`" + "\nline 2'"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
}
