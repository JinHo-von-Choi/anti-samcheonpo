package procscan

import "testing"

func TestInfrastructureDoesNotTreatProjectServersAsHosts(t *testing.T) {
	for _, c := range []struct{ exe, monitor, want string }{
		{"/home/u/node_modules/@openai/codex/vendor/x86_64/codex/codex", "/home/u/.local/bin/samcheonpo", "codex_host"},
		{"/home/u/node_modules/@openai/codex-linux-x64/vendor/x86_64/codex/codex", "", "codex_host"},
		{"/home/u/.local/bin/samcheonpo-hook", "/home/u/.local/bin/samcheonpo", "monitor_hook"},
		{"/w/project/codex", "/home/u/.local/bin/samcheonpo", ""},
		{"/w/project/server", "/home/u/.local/bin/samcheonpo", ""},
		{"/w/project/samcheonpo-hook", "/home/u/.local/bin/samcheonpo", ""},
	} {
		if got := InfrastructureRole(c.exe, c.monitor); got != c.want {
			t.Fatalf("%s: %q want %q", c.exe, got, c.want)
		}
	}
}
