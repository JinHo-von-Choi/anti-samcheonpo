package hookclient

import (
	"bufio"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestBootstrapRetainsFirstConnectionAndDoesNotRetryWarm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.sock")
	listener := make(chan net.Listener, 1)
	errCh := make(chan error, 1)
	spawned := 0
	conn, err := connectHook(path, func() {
		spawned++
		go func() {
			time.Sleep(20 * time.Millisecond)
			ln, e := net.Listen("unix", path)
			if e != nil {
				errCh <- e
				return
			}
			listener <- ln
		}()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var ln net.Listener
	select {
	case ln = <-listener:
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("missing listener")
	}
	defer ln.Close()
	received := make(chan string, 1)
	go func() {
		server, e := ln.Accept()
		if e != nil {
			received <- ""
			return
		}
		defer server.Close()
		server.SetDeadline(time.Now().Add(time.Second))
		line, _ := bufio.NewReader(server).ReadString('\n')
		received <- line
	}()
	fmt.Fprintln(conn, `{"event":"UserPromptSubmit","payload":{"prompt":"first event"}}`)
	if text := <-received; text != "{\"event\":\"UserPromptSubmit\",\"payload\":{\"prompt\":\"first event\"}}\n" {
		t.Fatal("first event lost", text)
	}
	warm, err := connectHook(path, func() { spawned++ })
	if err != nil {
		t.Fatal(err)
	}
	warm.Close()
	if spawned != 1 {
		t.Fatal("warm hook spawned another daemon")
	}
}

func TestBootstrapFailureHasBoundedWait(t *testing.T) {
	start := time.Now()
	conn, err := connectHook(filepath.Join(t.TempDir(), "missing.sock"), func() {})
	if conn != nil || err == nil || time.Since(start) > 2*time.Second {
		t.Fatal("unbounded or false successful startup", err)
	}
}
