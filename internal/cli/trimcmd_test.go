package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrimApplyKeepsTheOriginalWhenWordsWereAdded(t *testing.T) {
	dir := t.TempDir()
	original, result := filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md")
	write := func(p, text string) {
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func() error {
		c := trimCmd()
		c.SetArgs([]string{"--apply", original, result})
		c.SetOut(os.NewFile(0, os.DevNull))
		c.SilenceErrors, c.SilenceUsage = true, true
		return c.Execute()
	}
	write(original, "데몬은 소켓을 연다. 부가 설명 문장이다.")
	write(result, "완전히 새로운 약속을 적는다.")
	if err := run(); err == nil {
		t.Fatal("new words are refused")
	}
	if b, _ := os.ReadFile(original); string(b) != "데몬은 소켓을 연다. 부가 설명 문장이다." {
		t.Fatalf("the original stays: %q", b)
	}
	write(result, "데몬은 소켓을 연다.")
	if err := run(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(original); string(b) != "데몬은 소켓을 연다." {
		t.Fatalf("applied: %q", b)
	}
}
