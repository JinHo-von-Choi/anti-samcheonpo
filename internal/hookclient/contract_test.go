package hookclient

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sockpath"
)

// contractCase is one row of contract_cases.json. The same file drives the
// Rust transport, so a change in one implementation that the other does not
// follow fails here.
type contractCase struct {
	Name      string `json:"name"`
	Agent     string `json:"agent"`
	Event     string `json:"event"`
	Daemon    string `json:"daemon"`
	Response  string `json:"response"`
	Stdin     string `json:"stdin"`
	ExpectAck string `json:"expect_ack"`
	Expect    struct {
		ExitCode         int    `json:"exit_code"`
		Stdout           string `json:"stdout"`
		Status           string `json:"status"`
		ResponseReceived bool   `json:"response_received"`
	} `json:"expect"`
}

type contractSpec struct {
	Version int            `json:"version"`
	Cases   []contractCase `json:"cases"`
}

func loadContractSpec(t *testing.T) contractSpec {
	t.Helper()
	raw, err := os.ReadFile("contract_cases.json")
	if err != nil {
		t.Fatalf("계약 명세 읽기 실패: %v", err)
	}
	var spec contractSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("계약 명세 해석 실패: %v", err)
	}
	if len(spec.Cases) == 0 {
		t.Fatal("계약 명세에 사례가 없다")
	}
	return spec
}

var (
	hookBinaryOnce sync.Once
	hookBinaryPath string
	hookBinaryErr  error
)

// hookBinary builds the transport once per test run. The contract is about
// the program an agent runs, not about a library call.
func hookBinary(t *testing.T) string {
	t.Helper()
	hookBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "samcheonpo-hook-contract")
		if err != nil {
			hookBinaryErr = err
			return
		}
		out := filepath.Join(dir, "samcheonpo-hook"+ExeSuffix)
		cmd := exec.Command("go", "build", "-o", out, "../../cmd/samcheonpo-hook")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if combined, err := cmd.CombinedOutput(); err != nil {
			hookBinaryErr = fmt.Errorf("훅 빌드 실패: %v: %s", err, combined)
			return
		}
		hookBinaryPath = out
	})
	if hookBinaryErr != nil {
		t.Fatalf("%v", hookBinaryErr)
	}
	return hookBinaryPath
}

// fakeDaemon answers one request with the case's response and records the
// delivery receipt the client returns.
type fakeDaemon struct {
	listener net.Listener
	ack      chan string
}

func startFakeDaemon(t *testing.T, sock string, response string, wantAck bool) *fakeDaemon {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("가상 데몬 소켓 열기 실패: %v", err)
	}
	d := &fakeDaemon{listener: ln, ack: make(chan string, 1)}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		if _, err := reader.ReadBytes('\n'); err != nil {
			return
		}
		if _, err := conn.Write([]byte(response + "\n")); err != nil {
			return
		}
		if !wantAck {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		d.ack <- strings.TrimSpace(string(line))
	}()
	return d
}

func (d *fakeDaemon) close() { _ = d.listener.Close() }

func TestHookTransportContract(t *testing.T) {
	spec := loadContractSpec(t)
	bin := hookBinary(t)
	for _, c := range spec.Cases {
		t.Run(c.Name, func(t *testing.T) {
			home := t.TempDir()
			run := filepath.Join(home, "run")
			if err := os.MkdirAll(run, 0o700); err != nil {
				t.Fatalf("런 폴더 만들기 실패: %v", err)
			}
			// The hook resolves the socket path with the same rules, so the
			// fake daemon must listen where the hook looks. A home path
			// longer than the socket limit moves both to the short path.
			sock := sockpath.ForHome(home, os.TempDir(), sockpath.UserID())
			var daemon *fakeDaemon
			if c.Daemon == "up" {
				daemon = startFakeDaemon(t, sock, c.Response, c.ExpectAck != "")
				defer daemon.close()
			}
			cmd := exec.Command(bin, "hook", c.Event, c.Agent)
			cmd.Stdin = strings.NewReader(c.Stdin)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			cmd.Env = append(os.Environ(),
				"SAMCHEONPO_HOME="+home,
				"SAMCHEONPO_NO_SPAWN=1",
				"SAMCHEONPO_HOOK_DIAGNOSTICS=1",
			)
			err := cmd.Run()
			exitCode := 0
			if err != nil {
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					exitCode = exitErr.ExitCode()
				} else {
					t.Fatalf("훅 실행 실패: %v", err)
				}
			}
			if exitCode != c.Expect.ExitCode {
				t.Errorf("종료 코드 %d, 기대 %d", exitCode, c.Expect.ExitCode)
			}
			if stdout.String() != c.Expect.Stdout {
				t.Errorf("표준출력 %q, 기대 %q", stdout.String(), c.Expect.Stdout)
			}
			var outcome Outcome
			if err := json.Unmarshal([]byte(strings.TrimSpace(stderr.String())), &outcome); err != nil {
				t.Fatalf("진단 출력 해석 실패(%q): %v", stderr.String(), err)
			}
			if outcome.Status != c.Expect.Status {
				t.Errorf("상태 %q, 기대 %q", outcome.Status, c.Expect.Status)
			}
			if outcome.ResponseReceived != c.Expect.ResponseReceived {
				t.Errorf("응답 수신 %t, 기대 %t", outcome.ResponseReceived, c.Expect.ResponseReceived)
			}
			if c.ExpectAck != "" {
				select {
				case got := <-daemon.ack:
					if got != c.ExpectAck {
						t.Errorf("전달 확인 %q, 기대 %q", got, c.ExpectAck)
					}
				case <-time.After(time.Second):
					t.Errorf("전달 확인 회신을 받지 못했다 (기대 %q)", c.ExpectAck)
				}
			}
		})
	}
}

// The transport must never end with a non-zero exit code: Copilot reads a
// failed preToolUse hook as a denial.
func TestHookTransportAlwaysExitsZero(t *testing.T) {
	bin := hookBinary(t)
	home := t.TempDir()
	run := filepath.Join(home, "run")
	if err := os.MkdirAll(run, 0o700); err != nil {
		t.Fatalf("런 폴더 만들기 실패: %v", err)
	}
	for _, args := range [][]string{
		{},
		{"hook"},
		{"hook", "PreToolUse", "claude", "extra"},
		{"not-hook", "PreToolUse"},
		{"--version"},
	} {
		cmd := exec.Command(bin, args...)
		cmd.Stdin = strings.NewReader("{\"broken\":")
		cmd.Env = append(os.Environ(), "SAMCHEONPO_HOME="+home, "SAMCHEONPO_NO_SPAWN=1")
		if err := cmd.Run(); err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				t.Errorf("인자 %v: 종료 코드 %d, 기대 0", args, exitErr.ExitCode())
				continue
			}
			t.Errorf("인자 %v: 실행 실패 %v", args, err)
		}
	}
}
