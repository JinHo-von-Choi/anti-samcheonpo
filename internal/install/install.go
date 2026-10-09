// Package install installs and removes the agent integration.
package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/procgroup"
	"github.com/JinHo-von-Choi/anti-samcheonpo/plugins"
)

// MarketplaceName is the local marketplace that serves the plugin.
const MarketplaceName = "samcheonpo-local"

// State records what install changed so uninstall can undo exactly that.
type State struct {
	SettingsPath    string            `json:"settings_path"`
	SettingsExisted bool              `json:"settings_existed"`
	HadStatusLine   bool              `json:"had_status_line"`
	PrevStatusLine  json.RawMessage   `json:"prev_status_line,omitempty"`
	InstalledStatus json.RawMessage   `json:"installed_status_line"`
	PluginDir       string            `json:"plugin_dir"`
	PluginCLI       bool              `json:"plugin_cli"`
	InstalledAt     time.Time         `json:"installed_at"`
	Generated       map[string]string `json:"generated_sha256,omitempty"`
}

// Options configures install.
type Options struct {
	Binary       string // absolute path of the samcheonpo binary
	SettingsPath string // ~/.claude/settings.json
	UsePluginCLI bool   // run `claude plugin marketplace add / install`
	Out          func(string)
}

func statePath() string { return filepath.Join(config.Home(), "install-state.json") }

// DefaultSettings returns the Claude Code user settings path.
func DefaultSettings() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude", "settings.json")
}

// Install writes the plugin, registers it and wraps the status line.
func Install(o Options) (retErr error) {
	if o.Out == nil {
		o.Out = func(string) {}
	}
	if _, err := os.Lstat(statePath()); err == nil {
		return errors.New("이미 설치되어 있다. 다시 설치하려면 먼저 samcheonpo uninstall")
	} else if !os.IsNotExist(err) {
		return err
	}
	settings, raw, err := readSettings(o.SettingsPath)
	if err != nil {
		return err
	}
	root := filepath.Join(config.Home(), "plugin")
	pdir := filepath.Join(root, "claude-code")
	if err := procgroup.MkdirAllPrivate(config.Home()); err != nil {
		return err
	}
	if err := os.Mkdir(root, 0700); err != nil {
		return fmt.Errorf("기존 플러그인 경로를 덮어쓰지 않습니다: %w", err)
	}
	committed, settingsWritten := false, false
	st := State{SettingsPath: o.SettingsPath, PluginDir: pdir, InstalledAt: time.Now().UTC()}
	defer func() {
		if committed {
			return
		}
		if settingsWritten {
			current, _, err := readSettings(o.SettingsPath)
			if err == nil && jsonEqual(current["statusLine"], st.InstalledStatus) {
				if st.HadStatusLine {
					current["statusLine"] = st.PrevStatusLine
				} else {
					delete(current, "statusLine")
				}
				if len(current) == 0 && !st.SettingsExisted {
					err = os.Remove(o.SettingsPath)
				} else {
					err = writeSettings(o.SettingsPath, current)
				}
			}
			if err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("설정 복구 실패: %w", err))
			}
		}
		if err := os.RemoveAll(root); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("새 플러그인 파일 복구 실패: %w", err))
		}
	}()
	if err := writePlugin(pdir, o.Binary); err != nil {
		return err
	}
	if err := writeMarketplace(root); err != nil {
		return err
	}
	st.Generated, err = generatedFiles(root)
	if err != nil {
		return err
	}
	if err := backup(o.SettingsPath, raw); err != nil {
		return err
	}
	st.SettingsExisted = raw != nil
	prev, had := settings["statusLine"]
	st.HadStatusLine = had
	cmd := strconv.Quote(o.Binary) + " statusline"
	if had {
		st.PrevStatusLine = prev
		var sl struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(prev, &sl) == nil && sl.Command != "" {
			cmd += " --wrap " + shellQuote(sl.Command)
		}
	}
	installed, _ := json.Marshal(map[string]any{"type": "command", "command": cmd, "padding": 0})
	st.InstalledStatus = installed
	settings["statusLine"] = installed
	if err := writeSettings(o.SettingsPath, settings); err != nil {
		return err
	}
	settingsWritten = true
	b, _ := json.MarshalIndent(st, "", "  ")
	if err := writeAtomic(statePath(), b, 0600); err != nil {
		return err
	}
	committed = true
	o.Out("상태줄을 " + o.SettingsPath + "에 등록했다")
	if o.UsePluginCLI {
		if _, err := exec.LookPath("claude"); err == nil {
			// Record cleanup intent before changing the external plugin registry.
			st.PluginCLI = true
			b, _ := json.MarshalIndent(st, "", "  ")
			if err := writeAtomic(statePath(), b, 0600); err != nil {
				return err
			}
			if out, err := exec.Command("claude", "plugin", "marketplace", "add", root).CombinedOutput(); err != nil {
				o.Out("marketplace 등록 실패: " + strings.TrimSpace(string(out)))
				return fmt.Errorf("marketplace 등록 실패; 설치 기록을 보존했습니다: %w", err)
			} else if out, err := exec.Command("claude", "plugin", "install", "samcheonpo@"+MarketplaceName, "--scope", "user").CombinedOutput(); err != nil {
				o.Out("플러그인 설치 실패: " + strings.TrimSpace(string(out)))
				return fmt.Errorf("플러그인 등록 실패; 설치 기록을 보존했습니다: %w", err)
			} else {
				st.PluginCLI = true
				o.Out("Claude Code 플러그인 samcheonpo@" + MarketplaceName + "을 설치했다 (새 세션부터 적용)")
			}
		} else {
			o.Out("claude 명령을 찾지 못했다. Claude Code에서 /plugin marketplace add " + root + " 후 samcheonpo를 설치하면 된다")
		}
	}
	return nil
}

// Uninstall restores the settings value install replaced, when unchanged since.
func Uninstall(out func(string)) error {
	if out == nil {
		out = func(string) {}
	}
	b, err := os.ReadFile(statePath())
	if err != nil {
		return errors.New("설치 기록이 없다")
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return err
	}
	if st.PluginCLI {
		if _, err := exec.LookPath("claude"); err != nil {
			return errors.New("claude 명령이 없어 등록 해제를 확인할 수 없습니다; 설치 기록을 보존했습니다")
		}
		if err := exec.Command("claude", "plugin", "uninstall", "samcheonpo@"+MarketplaceName).Run(); err != nil {
			return fmt.Errorf("플러그인 등록 해제 실패; 설치 기록 보존: %w", err)
		}
		if err := exec.Command("claude", "plugin", "marketplace", "remove", MarketplaceName).Run(); err != nil {
			return fmt.Errorf("marketplace 등록 해제 실패; 설치 기록 보존: %w", err)
		}
	}
	settings, raw, err := readSettings(st.SettingsPath)
	if err != nil {
		return err
	}
	cur, has := settings["statusLine"]
	switch {
	case has && jsonEqual(cur, st.InstalledStatus):
		if err := backup(st.SettingsPath, raw); err != nil {
			return err
		}
		if st.HadStatusLine {
			settings["statusLine"] = st.PrevStatusLine
		} else {
			delete(settings, "statusLine")
		}
		if len(settings) == 0 && !st.SettingsExisted {
			// the file did not exist before install
			if err := os.Remove(st.SettingsPath); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else if err := writeSettings(st.SettingsPath, settings); err != nil {
			return err
		}
		out("상태줄 설정을 설치 전 값으로 되돌렸다")
	default:
		out("상태줄 설정이 설치 뒤에 바뀌어 건드리지 않았다")
	}
	if err := removeGenerated(filepath.Join(config.Home(), "plugin"), st.Generated, out); err != nil {
		return err
	}
	return os.Remove(statePath())
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return string(ja) == string(jb)
}

// readSettings parses settings.json preserving unknown keys as raw JSON.
func readSettings(p string) (map[string]json.RawMessage, []byte, error) {
	if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("설정 심볼릭 링크를 바꾸지 않습니다. 대상의 실제 경로를 지정하세요")
	}
	m := map[string]json.RawMessage{}
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return m, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return m, raw, nil
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil, fmt.Errorf("%s를 해석하지 못했다: %w", p, err)
	}
	if m == nil {
		return nil, nil, errors.New("설정의 최상위 값은 JSON 객체여야 합니다")
	}
	return m, raw, nil
}

func writeSettings(p string, m map[string]json.RawMessage) error {
	if len(m) == 0 {
		return writeAtomic(p, []byte("{}\n"), 0o600)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(p, append(b, '\n'), 0o600)
}

func backup(p string, raw []byte) error {
	if raw == nil {
		return nil
	}
	dir := filepath.Join(config.Home(), "backup")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, filepath.Base(p)+"."+time.Now().Format("20060102-150405.000")), raw, 0o600)
}

func writeAtomic(p string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".samcheonpo-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return procgroup.Rename(tmp, p)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// writePlugin copies the embedded plugin, pointing commands at the binary.
func writePlugin(dst, bin string) error {
	if _, err := os.Lstat(dst); err == nil {
		return errors.New("기존 플러그인 디렉터리를 덮어쓰지 않습니다")
	} else if !os.IsNotExist(err) {
		return err
	}
	q := strconv.Quote(bin)
	return fs.WalkDir(plugins.ClaudeCode, "claude-code", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, "claude-code")
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := plugins.ClaudeCode.ReadFile(p)
		if err != nil {
			return err
		}
		s := string(b)
		switch {
		case strings.HasSuffix(p, "hooks.json"):
			inner := jsonEsc(strconv.Quote(hookclient.Executable(bin)))
			s = strings.ReplaceAll(s, `"samcheonpo hook `, `"`+inner[1:len(inner)-1]+` hook `)
		case strings.HasSuffix(p, ".md"):
			s = strings.ReplaceAll(s, "!`samcheonpo cmd ", "!`"+q+" cmd ")
			s = strings.ReplaceAll(s, "Bash(samcheonpo cmd:*)", "Bash("+bin+" cmd:*)")
		}
		return os.WriteFile(target, []byte(s), 0o644)
	})
}

func jsonEsc(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func writeMarketplace(root string) error {
	m := map[string]any{
		"name":    MarketplaceName,
		"owner":   map[string]string{"name": "samcheonpo"},
		"plugins": []map[string]any{{"name": "samcheonpo", "source": "./claude-code", "description": "AI 코딩 에이전트 헛짓 탐지와 원화 영수증"}},
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, ".claude-plugin", "marketplace.json"), b, 0o644)
}
