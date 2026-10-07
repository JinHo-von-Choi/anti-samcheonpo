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
