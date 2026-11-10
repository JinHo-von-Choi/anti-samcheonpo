package live

import (
	"os"
	"testing"
)

// Fixtures use English prompts and assert the Korean messages; language
// selection has its own test.
func TestMain(m *testing.M) {
	os.Setenv("SAMCHEONPO_LANGUAGE", "ko")
	// fixtures start a process and stop right away
	leftoverMinAge = 0
	os.Exit(m.Run())
}
