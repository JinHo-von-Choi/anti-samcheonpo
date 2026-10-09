//go:build windows

package live

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/testutil/fakeexe"
)

func serveSamePeerOnly(t *testing.T, sock string) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if !samePeerUser(c) {
					return
				}
				_, _ = bufio.NewReader(c).ReadString('\n')
				_, _ = c.Write([]byte("ok\n"))
			}()
		}
	}()
}

func TestSamePeerUserAcceptsThisUser(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "p.sock")
	serveSamePeerOnly(t, sock)
	exe := fakeexe.Install(t, t.TempDir(), "dial", fakeexe.Spec{Behavior: "dial"})
	if out, err := exec.Command(exe, sock).CombinedOutput(); err != nil {
		t.Fatalf("a connection from this user is refused: %v %s", err, out)
	}
}

// Creates a second local account, so it only runs where SAMCHEONPO_TEST_PEER_USER=1.
func TestSamePeerUserRefusesAnotherUser(t *testing.T) {
	if os.Getenv("SAMCHEONPO_TEST_PEER_USER") != "1" {
		t.Skip("set SAMCHEONPO_TEST_PEER_USER=1 to create a local test account")
	}
	const name, password = "scpeer", "Pe3r-Test-9x!"
	_ = exec.Command("net", "user", name, "/delete").Run()
	if out, err := exec.Command("net", "user", name, password, "/add").CombinedOutput(); err != nil {
		t.Skipf("cannot create a local account: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("net", "user", name, "/delete").Run() })

	shared, err := os.MkdirTemp(`C:\Users\Public`, "scpeer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(shared) })
	if out, err := exec.Command("icacls", shared, "/grant", "Everyone:(OI)(CI)F").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	exe := fakeexe.Install(t, shared, "dial", fakeexe.Spec{Behavior: "dial"})
	for _, p := range []string{exe, exe + ".fake"} {
		if out, err := exec.Command("icacls", p, "/grant", "Everyone:RX").CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	sock := filepath.Join(shared, "p.sock")
	serveSamePeerOnly(t, sock)

	token, err := logonUser(name, password)
	if err != nil {
		t.Skipf("cannot log the test account on: %v", err)
	}
	defer token.Close()
	cmd := exec.Command(exe, sock)
	cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(token)}
	done := make(chan error, 1)
	var out []byte
	go func() { var err error; out, err = cmd.CombinedOutput(); done <- err }()
	select {
	case err := <-done:
		var ee *exec.ExitError
		if err == nil {
			t.Fatalf("another user's connection was answered: %s", out)
		}
		if ee, _ = err.(*exec.ExitError); ee == nil || ee.ExitCode() != 3 {
			t.Fatalf("the other user should connect and be dropped (exit 3): %v %s", err, out)
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the other user's client hung")
	}
}

// logonUser returns a primary token for a local account (LOGON32_LOGON_INTERACTIVE).
func logonUser(name, password string) (windows.Token, error) {
	const logonInteractive, providerDefault = 2, 0
	proc := windows.NewLazySystemDLL("advapi32.dll").NewProc("LogonUserW")
	user, domain, pass := windows.StringToUTF16Ptr(name), windows.StringToUTF16Ptr("."), windows.StringToUTF16Ptr(password)
	var token windows.Token
	r, _, err := proc.Call(uintptr(unsafe.Pointer(user)), uintptr(unsafe.Pointer(domain)), uintptr(unsafe.Pointer(pass)), logonInteractive, providerDefault, uintptr(unsafe.Pointer(&token)))
	if r == 0 {
		return 0, err
	}
	return token, nil
}
