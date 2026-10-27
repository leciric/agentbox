package machine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/incus"
)

// TestInfoReadsTheIncusInstance checks the adapter's translation, which is all
// it does: the manager reads State, IPv4 and LastUsedAt and nothing else.
func TestInfoReadsTheIncusInstance(t *testing.T) {
	inst := incus.Instance{
		Name:       "ab-demo-agent-01",
		Status:     "Running",
		LastUsedAt: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
	}
	got := info(inst)
	if got.Ref != inst.Name || got.State != "Running" || !got.Running() || got.Paused() {
		t.Errorf("info() = %+v, want a running machine named %s", got, inst.Name)
	}
	if !got.LastUsedAt.Equal(inst.LastUsedAt) {
		t.Errorf("info().LastUsedAt = %v, want %v", got.LastUsedAt, inst.LastUsedAt)
	}
}

// TestNotFoundKeepsItsCause checks that a caller can ask whether a machine is
// gone without importing Incus, and still read Incus' own words when it isn't.
func TestNotFoundKeepsItsCause(t *testing.T) {
	if err := notFound(nil); err != nil {
		t.Errorf("notFound(nil) = %v, want nil", err)
	}
	missing := notFound(fmt.Errorf("getting instance: %w", incus.ErrNotFound))
	if !errors.Is(missing, ErrNotFound) {
		t.Errorf("notFound(ErrNotFound) = %v, want ErrNotFound", missing)
	}
	other := notFound(errors.New("incus isn't answering"))
	if errors.Is(other, ErrNotFound) {
		t.Errorf("notFound(other) = %v, want it not to be ErrNotFound", other)
	}
	if other.Error() != "incus isn't answering" {
		t.Errorf("notFound(other) = %q, want the error unchanged", other)
	}
}

// TestIncusDialUsesTheHostSocket checks the proxy case: Dial reaches a socket
// on this host, and a socket that isn't there reads as ErrNotFound.
func TestIncusDialUsesTheHostSocket(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		_, _ = conn.Write([]byte("hi"))
		_ = conn.Close()
	}()
	m := IncusMachine{ref: "ab-demo-agent-01"}
	conn, err := m.Dial(context.Background(), "unix", socket)
	if err != nil {
		t.Fatalf("Dial = %v", err)
	}
	defer func() { _ = conn.Close() }()
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "hi" {
		t.Errorf("Dial read %q, %v, want hi", buf, err)
	}

	if _, err := m.Dial(context.Background(), "unix", filepath.Join(dir, "missing")); !errors.Is(err, ErrNotFound) {
		t.Errorf("Dial of a missing socket = %v, want ErrNotFound", err)
	}
}
