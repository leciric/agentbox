package incus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanOpenSocketFollowsWhoMayOpenIt(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "unix.socket")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("INCUS_SOCKET", socket)
	if !CanOpenSocket() {
		t.Error("CanOpenSocket() = false for a socket this user owns")
	}

	t.Setenv("INCUS_SOCKET", filepath.Join(dir, "gone"))
	if CanOpenSocket() {
		t.Error("CanOpenSocket() = true with no socket there: Incus isn't installed")
	}

	// What an unset group looks like before host setup's ACL: the file is
	// there, and this user may not open it.
	if os.Geteuid() == 0 {
		t.Skip("root may open anything")
	}
	if err := os.Chmod(socket, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Setenv("INCUS_SOCKET", socket)
	if CanOpenSocket() {
		t.Error("CanOpenSocket() = true for a socket this user may not open")
	}
}

func TestReachableNeedsTheCommandAndTheSocket(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "unix.socket")
	bin := filepath.Join(dir, "incus")
	for path, mode := range map[string]os.FileMode{socket: 0o600, bin: 0o755} {
		if err := os.WriteFile(path, nil, mode); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("INCUS_SOCKET", socket)

	if !(Client{Bin: bin}).Reachable() {
		t.Error("Reachable() = false with the command and the socket both there")
	}
	if (Client{Bin: filepath.Join(dir, "not-installed")}).Reachable() {
		t.Error("Reachable() = true with no incus command")
	}
	t.Setenv("INCUS_SOCKET", filepath.Join(dir, "gone"))
	if (Client{Bin: bin}).Reachable() {
		t.Error("Reachable() = true with no socket to open")
	}
}
