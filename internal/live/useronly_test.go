package live

import "testing"

func TestUserOnlyCommandMatching(t *testing.T) {
	for _, c := range []string{
		"samcheonpo cmd accept",
		"samcheonpo cmd keep normal",
		"samcheonpo cmd keep now",
		"samcheonpo cmd skip",
		"samcheonpo cmd edit 목표 변경",
		"samcheonpo cmd rollback apply",
		"samcheonpo contract accept",
		"~/.local/bin/samcheonpo cmd accept",
		`"/home/u/.local/bin/samcheonpo" cmd keep normal`,
		"cd /w && samcheonpo cmd accept",
		"samcheonpo --db /tmp/x.db cmd accept",
		"echo y | samcheonpo cmd accept",
	} {
		if !userOnlyCommand(c) {
			t.Errorf("%q must be refused", c)
		}
	}
	for _, c := range []string{
		"samcheonpo cmd summary",
		"samcheonpo cmd check",
		"samcheonpo cmd rollback",
		"samcheonpo cmd card",
		"samcheonpo contract show",
		"samcheonpo audit --since 30d",
		"grep -r 'samcheonpo cmd accept' docs/",
		"cat README.md",
		"samcheonpo-hook hook PreToolUse",
	} {
		if userOnlyCommand(c) {
			t.Errorf("%q must be allowed", c)
		}
	}
}
