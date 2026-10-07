//go:build linux

package live

import (
	"net"
	"path/filepath"
	"testing"
)

func TestSamePeerUserAcceptsOwnProcess(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := net.Dial("unix", sock); err == nil {
			defer c.Close()
			_, _ = c.Write([]byte("x"))
		}
	}()
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !samePeerUser(c) {
		t.Fatal("a connection from this user is accepted")
	}
	if samePeerUser(&net.TCPConn{}) {
		t.Fatal("a connection whose peer cannot be identified is refused")
	}
}
