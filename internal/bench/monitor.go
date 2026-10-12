package bench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	if mode != "shadow" && mode != "recommend" {
		return c, errors.New("benchmark monitor supports shadow or advisory recommend only")
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

// startTrialMonitor boots the selected build in a fresh experiment home. Old
// installed hook clients may connect through the same protocol; no user binary,
// model, authentication or permission setting is changed.
func startTrialMonitor(a Arm, repo, home string, env []string) (func(), string, string, error) {
	cfg, err := monitorConfig(a)
	if err != nil {
		return nil, "", "", err
	}
	f, err := os.Open(a.MonitorExecutable)
	if err != nil {
		return nil, "", "", err
	}
	if err = procgroup.MkdirAllPrivate(home); err != nil {
		f.Close()
		return nil, "", "", err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
		f.Close()
		return nil, "", "", errors.New("monitor executable must be a bounded regular file")
	}
	name := "samcheonpo-monitor"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	snapshot := filepath.Join(home, name)
	binary, err := os.OpenFile(snapshot, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		f.Close()
		return nil, "", "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(binary, h), io.LimitReader(f, (256<<20)+1))
	after, statErr := f.Stat()
	f.Close()
	closeErr := binary.Close()
	if err != nil || statErr != nil || closeErr != nil || n != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return nil, "", "", errors.New("monitor executable changed or snapshot failed")
	}
	binaryHash := hex.EncodeToString(h.Sum(nil))
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, "", "", err
	}
	configDigest := sha256.Sum256(raw)
	configHash := hex.EncodeToString(configDigest[:])
	configFile, err := os.OpenFile(filepath.Join(home, "config.yml"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, "", "", err
	}
	_, err = configFile.Write(raw)
	closeErr = configFile.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, "", "", err
	}
	log, err := os.OpenFile(filepath.Join(home, "monitor.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, "", "", err
	}
	cmd := exec.Command(snapshot, "daemon")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = log, log
	procgroup.Set(cmd)
	if err = procgroup.Start(cmd); err != nil {
		log.Close()
		return nil, "", "", err
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
			return nil, "", "", errors.New("selected trial monitor exited before readiness")
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
			return finish, binaryHash, configHash, nil
		}
	}
	finish()
	return nil, "", "", errors.New("selected trial monitor readiness timed out")
}
