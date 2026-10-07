package install

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
	"github.com/JinHo-von-Choi/anti-samcheonpo/plugins"
)

// OpenclawPlugin is the plugin id in OpenClaw.
const OpenclawPlugin = "samcheonpo"

// OpenclawSourceDir holds the plugin files OpenClaw installs from.
func OpenclawSourceDir() string { return filepath.Join(config.Home(), "openclaw-plugin") }

// openclawCLI runs the openclaw command; a variable for tests.
var openclawCLI = func(args ...string) ([]byte, error) {
	return exec.Command("openclaw", args...).CombinedOutput()
}

func openclawFiles(dir string) map[string][]byte {
	return map[string][]byte{
		filepath.Join(dir, "package.json"):         plugins.OpenclawPackage,
		filepath.Join(dir, "openclaw.plugin.json"): plugins.OpenclawManifest,
	}
}

// InstallOpenclaw writes the forwarder plugin and installs it with
// `openclaw plugins install`, which copies it into OpenClaw's extensions and
// records it in OpenClaw's config. The stop hook reads the final answer, so
// the plugin gets conversation access.
func InstallOpenclaw(bin string, out func(string)) error {
	bin = hookclient.Executable(bin)
	if out == nil {
		out = func(string) {}
	}
	dir := OpenclawSourceDir()
	files := openclawFiles(dir)
	files[filepath.Join(dir, "index.js")] = []byte(strings.Replace(string(plugins.OpenclawIndex),
		`process.env.SAMCHEONPO_BIN ?? "samcheonpo"`, `process.env.SAMCHEONPO_BIN ?? `+strconv.Quote(bin), 1))
	var written []string
	undo := func() {
		for _, p := range written {
			_ = removeStandalone("openclaw", p)
		}
		_ = os.Remove(dir)
	}
	for p, body := range files {
		if err := installStandalone("openclaw", p, body); err != nil {
			undo()
			return err
		}
		written = append(written, p)
	}
	if b, err := openclawCLI("plugins", "install", dir); err != nil {
		undo()
		return errors.New("openclaw plugins install 실패: " + strings.TrimSpace(string(b)) + " (" + err.Error() + ")")
	}
	if b, err := openclawCLI("config", "set", "plugins.entries."+OpenclawPlugin+".hooks.allowConversationAccess", "true"); err != nil {
		_, _ = openclawCLI("plugins", "uninstall", OpenclawPlugin, "--force")
		undo()
		return errors.New("openclaw config set 실패: " + strings.TrimSpace(string(b)) + " (" + err.Error() + ")")
	}
	out("OpenClaw 플러그인 " + OpenclawPlugin + "를 설치했다 (새 실행부터 적용, 게이트웨이는 재시작 후 적용)")
	return nil
}

// UninstallOpenclaw removes the plugin through `openclaw plugins uninstall`
// (extension directory, config entry, allowlist entry) and the source files.
func UninstallOpenclaw(out func(string)) error {
	if out == nil {
		out = func(string) {}
	}
	dir := OpenclawSourceDir()
	paths := []string{filepath.Join(dir, "index.js")}
	for p := range openclawFiles(dir) {
		paths = append(paths, p)
	}
	for _, p := range paths {
		if _, err := checkStandalone("openclaw", p); err != nil {
			return err
		}
	}
	if b, err := openclawCLI("plugins", "uninstall", OpenclawPlugin, "--force"); err != nil {
		return errors.New("openclaw plugins uninstall 실패: " + strings.TrimSpace(string(b)) + " (" + err.Error() + ")")
	}
	for _, p := range paths {
		if err := removeStandalone("openclaw", p); err != nil {
			return err
		}
	}
	_ = os.Remove(dir)
	out("OpenClaw 플러그인 " + OpenclawPlugin + "를 제거했다")
	return nil
}
