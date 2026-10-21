package install

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
	"github.com/JinHo-von-Choi/anti-samcheonpo/plugins"
)

// HermesPlugin is the plugin name in Hermes (directory and plugins.enabled).
const HermesPlugin = "samcheonpo"

// HermesPluginDir is the forwarder plugin directory in the Hermes home.
func HermesPluginDir() string {
	home := os.Getenv("HERMES_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".hermes")
	}
	return filepath.Join(home, "plugins", HermesPlugin)
}

// hermesCLI runs the hermes command; a variable for tests.
var hermesCLI = func(args ...string) ([]byte, error) {
	return exec.Command("hermes", args...).CombinedOutput()
}

// InstallHermes writes the forwarder plugin and enables it with
// `hermes plugins enable`, which edits Hermes' own config.
func InstallHermes(bin, dir string, out func(string)) error {
	bin = hookclient.Executable(bin)
	if out == nil {
		out = func(string) {}
	}
	manifest, init := filepath.Join(dir, "plugin.yaml"), filepath.Join(dir, "__init__.py")
	src := strings.Replace(string(plugins.HermesInit), `os.environ.get("SAMCHEONPO_BIN") or "samcheonpo"`, `os.environ.get("SAMCHEONPO_BIN") or `+strconv.Quote(bin), 1)
	if err := installStandalone("hermes", manifest, plugins.HermesManifest); err != nil {
		return err
	}
	if err := installStandalone("hermes", init, []byte(src)); err != nil {
		_ = removeStandalone("hermes", manifest)
		return err
	}
	// The source was just rewritten, so any bytecode cache left from an
	// earlier version belongs to the old source. Python would discard it,
	// but the integrity record must not keep a stale cache either.
	_ = os.RemoveAll(filepath.Join(dir, "__pycache__"))
	if b, err := hermesCLI("plugins", "enable", HermesPlugin); err != nil {
		_ = removeStandalone("hermes", init)
		_ = removeStandalone("hermes", manifest)
		_ = os.Remove(dir)
		return errors.New("hermes plugins enable 실패: " + strings.TrimSpace(string(b)) + " (" + err.Error() + ")")
	}
	// The bundle is recorded so that a later change to the installed files
	// is visible instead of silent.
	if m, err := BuildManifest("hermes", dir, []string{manifest, init}, true); err != nil {
		out("무결성 기록을 남기지 못했다: " + err.Error())
	} else if err := RecordManifest(m); err != nil {
		out("무결성 기록을 남기지 못했다: " + err.Error())
	}
	out("Hermes 플러그인을 " + dir + "에 설치하고 켰다 (새 세션부터 적용, 게이트웨이는 재시작 후 적용)")
	return nil
}

// UninstallHermes removes the plugin if its files are unchanged, through
// `hermes plugins remove` so the plugins.enabled entry goes too.
func UninstallHermes(out func(string)) error {
	if out == nil {
		out = func(string) {}
	}
	dir := HermesPluginDir()
	manifest, init := filepath.Join(dir, "plugin.yaml"), filepath.Join(dir, "__init__.py")
	for _, p := range []string{manifest, init} {
		if _, err := checkStandalone("hermes", p); err != nil {
			return err
		}
	}
	if b, err := hermesCLI("plugins", "remove", HermesPlugin); err != nil {
		// without the hermes command: remove the files, the config entry stays
		for _, p := range []string{init, manifest} {
			if err := removeStandalone("hermes", p); err != nil {
				return err
			}
		}
		_ = os.RemoveAll(filepath.Join(dir, "__pycache__"))
		_ = os.Remove(dir)
		out("플러그인 파일을 지웠다. hermes plugins remove가 실패해 plugins.enabled의 " + HermesPlugin + " 항목은 직접 지워야 한다: " + strings.TrimSpace(string(b)))
		return nil
	}
	for _, p := range []string{init, manifest} {
		if err := removeStandalone("hermes", p); err != nil {
			return err
		}
	}
	if err := ForgetManifest("hermes"); err != nil {
		out("무결성 기록을 지우지 못했다: " + err.Error())
	}
	out("Hermes 플러그인 " + HermesPlugin + "를 제거했다")
	return nil
}
