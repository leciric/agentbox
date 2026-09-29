package chv

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeVsock is Cloud Hypervisor's hybrid vsock socket: it takes
// "CONNECT <port>\n" and answers "OK <n>\n" for the ports it has, then
// echoes back everything it reads once the host's side has half-closed.
// Ports it doesn't have are closed without an answer, as when nothing in the
// VM listens.
func fakeVsock(t *testing.T, ports ...uint32) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "vsock.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				r := bufio.NewReader(c)
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				var port uint32
				if _, err := fmt.Sscanf(line, "CONNECT %d\n", &port); err != nil {
					return
				}
				known := false
				for _, p := range ports {
					known = known || p == port
				}
				if !known {
					return
				}
				// The answer and the stream's first bytes in one write,
				// as a guest that speaks first (sshd) would have it.
				_, _ = fmt.Fprintf(c, "OK 1073741824\nport %d\n", port)
				data, _ := io.ReadAll(r)
				_, _ = c.Write(data)
			}()
		}
	}()
	return socket
}

func TestDialVsock(t *testing.T) {
	socket := fakeVsock(t, PortSSH)
	conn, err := dialVsock(context.Background(), socket, PortSSH)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = io.WriteString(conn, "hello")
	_ = conn.CloseWrite()
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "port 22\nhello" {
		t.Errorf("read %q: the handshake ate or kept part of the stream", got)
	}
}

func TestDialVsockRefused(t *testing.T) {
	socket := fakeVsock(t, PortSSH)
	_, err := dialVsock(context.Background(), socket, PortDaemon)
	if !errors.Is(err, errVsockRefused) {
		t.Errorf("dialVsock = %v, want errVsockRefused", err)
	}
	if _, err := dialVsock(context.Background(), filepath.Join(t.TempDir(), "none"), PortSSH); err == nil {
		t.Error("dialVsock succeeded with no VM")
	}
}

func TestForward(t *testing.T) {
	socket := fakeVsock(t, PortDaemon)
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "agentbox.sock"))
	if err != nil {
		t.Fatal(err)
	}
	var paused atomic.Bool
	f := &forward{
		name: "daemon socket", ln: ln,
		dial: func(ctx context.Context) (net.Conn, error) { return dialVsock(ctx, socket, PortDaemon) },
		open: func() bool { return !paused.Load() },
		logf: t.Logf,
	}
	done := make(chan struct{})
	go func() { f.serve(context.Background()); close(done) }()

	// A megabyte there and back, the request half-closed first.
	payload := make([]byte, 1<<20)
	_, _ = rand.Read(payload)
	c, err := net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = c.Write(payload)
		_ = c.(*net.UnixConn).CloseWrite()
	}()
	got, err := io.ReadAll(c)
	_ = c.Close()
	if err != nil {
		t.Fatal(err)
	}
	if want := append([]byte("port 1024\n"), payload...); !bytes.Equal(got, want) {
		t.Fatalf("got %d bytes back, want %d", len(got), len(want))
	}

	// Paused, a connection is closed at once rather than left hanging.
	paused.Store(true)
	c, err = net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := c.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Errorf("paused: read %d, %v; want EOF at once", n, err)
	}
	_ = c.Close()

	_ = ln.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serve didn't return once its listener closed")
	}
}

func TestProxy(t *testing.T) {
	socket := fakeVsock(t, PortSSH)
	conn, err := dialVsock(context.Background(), socket, PortSSH)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := proxy(context.Background(), conn, strings.NewReader("SSH-2.0-client\n"), &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "port 22\nSSH-2.0-client\n" {
		t.Errorf("proxy wrote %q", out.String())
	}
}

func TestProxyEndsWithTheVM(t *testing.T) {
	a, b := net.Pipe()
	stdin, stdinW := io.Pipe() // never closes, like a terminal
	defer func() { _ = stdinW.Close() }()
	done := make(chan error, 1)
	go func() { done <- proxy(context.Background(), a, stdin, io.Discard) }()
	_ = b.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("proxy outlived the VM's side")
	}
}
