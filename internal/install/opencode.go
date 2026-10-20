package install

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/plugins"
)

// OpencodePluginPath is the global opencode plugin file.
func OpencodePluginPath() string {
	if d := os.Getenv("OPENCODE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "plugins", "samcheonpo.ts")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "opencode", "plugins", "samcheonpo.ts")
}

const opencodeMarker = "// samcheonpo forwarder for opencode"

// InstallOpencode writes the forwarder plugin with the binary path.
func InstallOpencode(bin, path string, out func(string)) error {
	bin = hookclient.Executable(bin)
	if out == nil {
		out = func(string) {}
	}
	src := strings.Replace(string(plugins.Opencode), `process.env.SAMCHEONPO_BIN ?? "samcheonpo"`, `process.env.SAMCHEONPO_BIN ?? `+strconv.Quote(bin), 1)
	if err := installStandalone("opencode", path, []byte(src)); err != nil {
		return err
	}
	// The bundle is recorded so that a later change to the installed file
	// is visible instead of silent.
	if m, err := BuildManifest("opencode", filepath.Dir(path), []string{path}, false); err != nil {
		out("무결성 기록을 남기지 못했다: " + err.Error())
	} else if err := RecordManifest(m); err != nil {
		out("무결성 기록을 남기지 못했다: " + err.Error())
	}
	out("opencode 플러그인을 " + path + "에 설치했다 (새 세션부터 적용)")
	return nil
}

// UninstallOpencode removes the forwarder plugin written by InstallOpencode.
func UninstallOpencode(out func(string)) error {
	p := OpencodePluginPath()
	if err := removeStandalone("opencode", p); err != nil {
		return err
	}
	if err := ForgetManifest("opencode"); err != nil {
		out("무결성 기록을 지우지 못했다: " + err.Error())
	}
	if out != nil {
		out(p + "를 지웠다")
	}
	return nil
}
