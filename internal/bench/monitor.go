package bench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/policy"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/procgroup"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sockpath"
	"gopkg.in/yaml.v3"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
)

func monitorConfig(a Arm) (config.Config, error) {
	c := config.Default()
	if a.Monitor != "samcheonpo" || !filepath.IsAbs(a.MonitorExecutable) {
		return c, errors.New("explicit monitor executable requires samcheonpo and an absolute binary path")
	}
	mode := a.MonitorRollout
	if mode == "" {
		mode = "shadow"
	}
	if mode != "shadow" && mode != "recommend" && mode != "default" {
		return c, errors.New("benchmark monitor supports shadow, advisory recommend, or explicit product default")
	}
	if mode == "default" {
		if len(a.RecommendRules) != 0 || a.RulePack != "" {
			return c, errors.New("default profile cannot add experimental rules or packs")
		}
		return c, nil // exact product defaults, including bounded escalation
	}
	if len(a.RecommendRules) > 32 || mode == "shadow" && len(a.RecommendRules) > 0 {
		return c, errors.New("explicit advisory rules require recommend mode; at most 32")
	}
	for _, rule := range a.RecommendRules {
		if !policy.AdviceOnly(rule) {
			return c, errors.New("benchmark opt-in must name advisory-only rules")
		}
	}
	c.Rollout.Mode = mode
	c.Rollout.EscalateMaxBlocks = 0
	c.Rollout.RecommendRules = append([]string(nil), a.RecommendRules...)
	return c, nil
}

// MonitorFingerprint returns the digest of what a trial monitor was.
//
// It covers the binary, the configuration and the learned rule pack
// together. A comparison that changes the rule pack changes the
// fingerprint, and a comparison that changes nothing else does not.
func MonitorFingerprint(binaryHash, configHash, packHash string) string {
	h := sha256.Sum256([]byte(binaryHash + "/" + configHash + "/" + packHash))
	return hex.EncodeToString(h[:])
}

// trialPolicyHash identifies the policy independently of the newly allocated
// grader directory. When the runner supplies grader protection, it replaces
// ProtectedPaths exactly as the emitted configuration does, using stable roles.
// Without that override the policy's own paths are hashed unchanged. The exact
// emitted configuration is separately retained as MonitorConfigSHA256.
func trialPolicyHash(c config.Config, graderPaths []string) (string, error) {
	if len(graderPaths) > 0 {
		c.ProtectedPaths = make([]string, len(graderPaths))
		for i := range graderPaths {
			c.ProtectedPaths[i] = fmt.Sprintf("<trial-grader-%d>", i)
		}
	}
	raw, err := yaml.Marshal(c)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(append([]byte("trial-policy/1\n"), raw...))
	return hex.EncodeToString(h[:]), nil
}

// startTrialMonitor boots the selected build in a fresh experiment home. Old
// installed hook clients may connect through the same protocol; no user binary,
// model, authentication or permission setting is changed.
//
// It returns the binary digest, the configuration digest and the monitor
// fingerprint. The fingerprint covers the binary, the configuration and the
// learned rule pack together, so two arms can be compared by what the
// monitor actually was rather than by what someone wrote down about it.
//
// The protected paths are the ones the trial must not reach, the grader
// above all. A monitor that knows them stops the call before it runs,
// which is the difference between a warning and a block.
func startTrialMonitor(a Arm, repo, home string, env []string, protected []string) (func(), string, string, string, error) {
	cfg, err := monitorConfig(a)
	if err != nil {
		return nil, "", "", "", err
	}
	policyHash, err := trialPolicyHash(cfg, protected)
	if err != nil {
		return nil, "", "", "", err
	}
	if len(protected) > 0 {
		cfg.ProtectedPaths = append([]string(nil), protected...)
	}
	f, err := os.Open(a.MonitorExecutable)
	if err != nil {
		return nil, "", "", "", err
	}
	if err = procgroup.MkdirAllPrivate(home); err != nil {
		f.Close()
		return nil, "", "", "", err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
		f.Close()
		return nil, "", "", "", errors.New("monitor executable must be a bounded regular file")
	}
	name := "samcheonpo-monitor"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	snapshot := filepath.Join(home, name)
	binary, err := os.OpenFile(snapshot, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		f.Close()
		return nil, "", "", "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(binary, h), io.LimitReader(f, (256<<20)+1))
	after, statErr := f.Stat()
	f.Close()
	closeErr := binary.Close()
	if err != nil || statErr != nil || closeErr != nil || n != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return nil, "", "", "", errors.New("monitor executable changed or snapshot failed")
	}
	binaryHash := hex.EncodeToString(h.Sum(nil))
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, "", "", "", err
	}
	configDigest := sha256.Sum256(raw)
	configHash := hex.EncodeToString(configDigest[:])
	// The learned rule pack is part of what the monitor is. A pack that
	// is not part of the fingerprint is a pack that can differ between
	// two arms without the comparison noticing.
	packHash := ""
	if a.RulePack != "" {
		pack, err := os.ReadFile(a.RulePack)
		if err != nil {
			return nil, "", "", "", fmt.Errorf("후보 규칙 팩을 읽지 못했다: %w", err)
		}
		if len(pack) > 1<<20 {
			return nil, "", "", "", errors.New("후보 규칙 팩이 너무 크다")
		}
		packDigest := sha256.Sum256(pack)
		packHash = hex.EncodeToString(packDigest[:])
		if err := os.WriteFile(filepath.Join(home, "learned-rules.json"), pack, 0o600); err != nil {
			return nil, "", "", "", err
		}
	}
	fingerprint := MonitorFingerprint(binaryHash, policyHash, packHash)
	configFile, err := os.OpenFile(filepath.Join(home, "config.yml"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, "", "", "", err
	}
	_, err = configFile.Write(raw)
	closeErr = configFile.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, "", "", "", err
	}
	log, err := os.OpenFile(filepath.Join(home, "monitor.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, "", "", "", err
	}
	cmd := exec.Command(snapshot, "daemon")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = log, log
	procgroup.Set(cmd)
	if err = procgroup.Start(cmd); err != nil {
		log.Close()
		return nil, "", "", "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	var once sync.Once
	finish := func() {
		once.Do(func() {
			select {
			case <-done:
			default:
				_ = cmd.Process.Signal(syscall.SIGTERM)
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					_ = procgroup.Kill(cmd)
					<-done
				}
			}
			procgroup.Release(cmd)
			log.Close()
		})
	}
	tmp := os.TempDir()
	if v := a.Env["TMPDIR"]; v != "" {
		tmp = v
	}
	socket := sockpath.ForHome(home, tmp, sockpath.UserID())
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		select {
		case <-done:
			finish()
			return nil, "", "", "", errors.New("selected trial monitor exited before readiness")
		default:
		}
		conn, e := net.DialTimeout("unix", socket, 50*time.Millisecond)
		if e != nil {
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(50 * time.Millisecond))
		e = json.NewEncoder(conn).Encode(map[string]any{"v": 1, "event": "Ping", "payload": map[string]any{}})
		var reply struct {
			Text  string `json:"text"`
			Error string `json:"error"`
		}
		if e == nil {
			e = json.NewDecoder(conn).Decode(&reply)
		}
		conn.Close()
		if e == nil && reply.Text == "pong" && reply.Error == "" {
			return finish, binaryHash, configHash, fingerprint, nil
		}
	}
	finish()
	return nil, "", "", "", errors.New("selected trial monitor readiness timed out")
}
