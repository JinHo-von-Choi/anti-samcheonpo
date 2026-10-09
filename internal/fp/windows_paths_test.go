package fp

import "testing"

func TestExecFPTreatsDriveLetterDirectoriesAsUnknown(t *testing.T) {
	for _, cmd := range []string{`cd C:\work\app && go test ./...`, `cd "C:/work/app" && go test ./...`, `cd \\server\share && go test ./...`} {
		if _, certain := ExecFP(cmd, ""); certain {
			t.Errorf("%q changes directory to a place the harness cannot place in the project", cmd)
		}
	}
	if _, certain := ExecFP("cd app && go test ./...", ""); !certain {
		t.Error("a relative directory stays comparable")
	}
}

func TestNormalizeCmdReducesWindowsSpellingsToTheProgramName(t *testing.T) {
	for in, want := range map[string]string{
		`"C:\Program Files\nodejs\npm.cmd" test`: "npm test",
		`C:\Python313\python.exe -m pytest -q`:   "python -m pytest -q",
		`pytest.exe  -q`:                         "pytest -q",
		`.\gradlew.bat test`:                     "gradlew test",
		"pytest -q":                              "pytest -q",
		"/usr/bin/pytest -q":                     "/usr/bin/pytest -q",
	} {
		if got, _ := NormalizeCmd(in); got != want {
			t.Errorf("NormalizeCmd(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestErrorFingerprintIgnoresTheDirectoryOfWindowsPaths(t *testing.T) {
	a := "Traceback (most recent call last):\n  File \"C:\\Users\\a\\proj1\\src\\app.py\", line 3, in run\nNameError: name 'y' is not defined in C:\\Users\\a\\proj1\\src\\app.py"
	b := "Traceback (most recent call last):\n  File \"D:\\work\\other\\src\\app.py\", line 3, in run\nNameError: name 'y' is not defined in D:\\work\\other\\src\\app.py"
	fa, fb := ErrorFPs(a), ErrorFPs(b)
	if len(fa) == 0 || len(fa) != len(fb) || fa[0] != fb[0] {
		t.Fatalf("the same error in two checkouts must share a fingerprint: %v vs %v", fa, fb)
	}
	if got := Template(`cannot open C:\Users\a\x\data.csv`); got != "cannot open <path>/data.csv" {
		t.Fatalf("template %q", got)
	}
}
