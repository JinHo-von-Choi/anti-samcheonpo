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
		"samcheonpo cmd extend 2h",
		"samcheonpo contract accept",
		"~/.local/bin/samcheonpo cmd accept",
		`"/home/u/.local/bin/samcheonpo" cmd keep normal`,
		"cd /w && samcheonpo cmd accept",
		"samcheonpo --db /tmp/x.db cmd accept",
		"echo y | samcheonpo cmd accept",
		"samcheonpo.exe cmd accept",
		`"C:\Users\u\bin\samcheonpo.exe" cmd keep normal`,
		`C:\Users\u\bin\SAMCHEONPO.EXE cmd skip`,
		"C:/Users/u/bin/samcheonpo.exe cmd rollback apply",
		".\\samcheonpo cmd accept",
		"bash -c 'samcheonpo cmd accept'",
		`sh -lc "cd /w && samcheonpo cmd keep normal"`,
		"cmd /c samcheonpo.exe cmd accept",
		`powershell -NoProfile -Command "samcheonpo.exe cmd skip"`,
		"bash <<'EOF'\nsamcheonpo cmd accept\nEOF",
		"python3 - <<'PY'\nimport subprocess\nsubprocess.run(['samcheonpo','cmd','keep','normal'])\nPY",
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
		"samcheonpo-hook.exe hook PreToolUse",
		"bash -c 'echo samcheonpo cmd accept'",
		"bash run.sh samcheonpo cmd accept",
		"cmd /c echo hello",
		"cat >> docs/GUIDE.md <<'EOF'\n| 해제 | `samcheonpo cmd keep normal`은 에이전트 셸에서 거부된다 |\nEOF",
		"python3 - <<'PY'\np.write_text(s.replace('`samcheonpo cmd accept`', '`samcheonpo cmd accept` 설명'))\nPY",
	} {
		if userOnlyCommand(c) {
			t.Errorf("%q must be allowed", c)
		}
	}
}
