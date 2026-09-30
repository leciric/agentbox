package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestASecondDaemonDoesNotStartBesideTheFirst(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(t.TempDir(), "agentbox.sock")
	unlock, err := lockDaemon(socket, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	_, err = lockDaemon(socket, 200*time.Millisecond)
	if want := fmt.Sprintf("(pid %d) is still running", os.Getpid()); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("lockDaemon() while another daemon holds it = %v, want an error with %q", err, want)
	}
	if WaitStopped(socket, 0) {
		t.Error("WaitStopped() = true while a daemon holds the lock")
	}
}

// The restart that left a daemon without its socket: the old daemon had
// stopped answering, but was still rolling its jobs back when the new one
// started, and removed the socket as it exited.
func TestTheNextDaemonWaitsForTheOneStopping(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(t.TempDir(), "agentbox.sock")
	unlockOld, err := lockDaemon(socket, 0)
	if err != nil {
		t.Fatal(err)
	}
	oldLn, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	removeOld := removeOwnSocket(socket)
	_ = oldLn.Close() // it no longer answers, but it runs on

	stopped := make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		removeOld()
		unlockOld()
		close(stopped)
	}()
	unlockNew, err := lockDaemon(socket, 5*time.Second)
	if err != nil {
		t.Fatalf("lockDaemon() while the old daemon stops = %v", err)
	}
	defer unlockNew()
	select {
	case <-stopped:
	default:
		t.Fatal("the new daemon got the lock before the old one let go of it")
	}
	if !WaitStopped(socket+".other", 0) {
		t.Error("WaitStopped() = false for a socket no daemon ever had")
	}
}

func TestADaemonRemovesOnlyItsOwnSocket(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(t.TempDir(), "agentbox.sock")
	old, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	old.(*net.UnixListener).SetUnlinkOnClose(false)
	removeOld := removeOwnSocket(socket)
	_ = old.Close()

	// A daemon without the lock, from an older AgentBox, took the path over.
	_ = os.Remove(socket)
	next, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = next.Close() }()
	removeOld()
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("the old daemon removed the new one's socket: %v", err)
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("the new daemon's socket doesn't answer: %v", err)
	}
	_ = conn.Close()

	removeNext := removeOwnSocket(socket)
	removeNext()
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Errorf("a daemon's own socket is still there after it stopped: %v", err)
	}
}
