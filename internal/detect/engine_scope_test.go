package detect

import (
	"reflect"
	"testing"
)

func TestGuessScopeWholeExtension(t *testing.T) {
	cases := map[string][]string{
		"app.cjs의 solve가 틀린다. SPEC.md대로 고쳐라": {"app.cjs", "SPEC.md"},
		"config.json 값을 읽는 loader.mjs를 고쳐":   {"config.json", "loader.mjs"},
		"src/a.py 시험 통과시켜 줘":                 {"src/**"},
		"버전 v1.2 기준으로 정리":                    nil,
	}
	for prompt, want := range cases {
		if got := GuessScope(prompt); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v, want %v", prompt, got, want)
		}
	}
}

func TestIsToolStatePath(t *testing.T) {
	for p, want := range map[string]bool{
		".idea/modules.xml":             true,
		"./.vscode/settings.json":       true,
		"app/app.iml":                   true,
		"web/node_modules/x/index.js":   true,
		"svc/target/classes/A.class":    true,
		".gradle/8.5/fileHashes.lock":   true,
		"pkg/__pycache__/m.cpython.pyc": true,
		`.idea\workspace.xml`:           true,
		"src/main/java/app/A.java":      false,
		"cmd/build/main.go":             false,
		"distance.go":                   false,
		"targets/list.py":               false,
	} {
		if got := IsToolStatePath(p); got != want {
			t.Errorf("IsToolStatePath(%q) = %v, want %v", p, got, want)
		}
	}
}
