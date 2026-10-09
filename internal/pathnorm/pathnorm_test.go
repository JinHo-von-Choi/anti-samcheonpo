package pathnorm

import "testing"

func TestCommandBase(t *testing.T) {
	for in, want := range map[string]string{
		"samcheonpo":                             "samcheonpo",
		"./samcheonpo":                           "samcheonpo",
		"/usr/local/bin/samcheonpo":              "samcheonpo",
		`C:\tools\samcheonpo.exe`:                "samcheonpo",
		`C:/tools/SAMCHEONPO.EXE`:                "samcheonpo",
		`.\samcheonpo.cmd`:                       "samcheonpo",
		"go.exe":                                 "go",
		"  pytest ":                              "pytest",
		"":                                       "",
		`\\server\share\bin\samcheonpo-hook.exe`: "samcheonpo-hook",
	} {
		if got := CommandBase(in); got != want {
			t.Errorf("CommandBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsWindowsAbs(t *testing.T) {
	for in, want := range map[string]bool{
		`C:\x`: true, "c:/x": true, `\\server\share`: true, "/x": false, "x": false, "a:": true, "1:x": false,
	} {
		if got := IsWindowsAbs(in); got != want {
			t.Errorf("IsWindowsAbs(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsAbs(t *testing.T) {
	for in, want := range map[string]bool{
		"/x": true, "C:/x": true, `C:\x`: true, `\\h\s`: true, "src/a.py": false, "./a": false, "../a": false, "": false,
	} {
		if got := IsAbs(in); got != want {
			t.Errorf("IsAbs(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsTempPath(t *testing.T) {
	for in, want := range map[string]bool{
		"/tmp/x":                          true,
		"/var/tmp/x":                      true,
		"/private/var/folders/ab/cd/T/x":  true,
		"C:/Users/u/AppData/Local/Temp/x": true,
		"c:/users/u/appdata/local/temp/x": true,
		"C:/Windows/Temp/x":               true,
		"C:/Users/u/project/x":            false,
		"/home/u/tmp/x":                   false,
		"src/tmp/x":                       false,
	} {
		if got := IsTempPath(in); got != want {
			t.Errorf("IsTempPath(%q) = %v, want %v", in, got, want)
		}
	}
}
