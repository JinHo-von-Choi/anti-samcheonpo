package contract

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "samcheonpo-contract-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("SAMCHEONPO_HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
