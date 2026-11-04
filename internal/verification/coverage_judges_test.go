package verification

import "testing"

func TestJudgesOnlyCommandsCoversPathModels(t *testing.T) {
	for cmd, want := range map[string]bool{
		"go test ./unrelated":     true,
		"pytest -q":               true,
		"CI=1 npm test":           true,
		"python -m unittest":      true,
		"./gradlew build":         false,
		"mvn -q test":             false,
		"pytest -q | tail -3":     false,
		"cd sub && go test ./...": false,
		"":                        false,
	} {
		if got := Judges(cmd); got != want {
			t.Errorf("Judges(%q) = %v, want %v", cmd, got, want)
		}
	}
}
