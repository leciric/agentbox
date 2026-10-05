package machines

import (
	"io"
	"os/exec"
	"strings"
	"testing"
)

func TestDialCommand(t *testing.T) {
	c, err := DialCommand(exec.Command("cat"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("RFB 003.008\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 12)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "RFB 003.008\n" {
		t.Fatalf("read %q, %v", buf, err)
	}
	if err := c.Close(); err != nil {
		t.Error(err)
	}
	if err := c.Close(); err != nil {
		t.Error("a second close:", err)
	}
}

// TestDialCommandFails says why the relay ended: a display that isn't up,
// say, rather than a bare EOF.
func TestDialCommandFails(t *testing.T) {
	c, err := DialCommand(exec.Command("sh", "-c", "echo connect ECONNREFUSED 127.0.0.1:5900 >&2; exit 1"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, err = io.ReadAll(c)
	if err == nil || !strings.Contains(err.Error(), "ECONNREFUSED") {
		t.Errorf("read: %v", err)
	}
}

func TestParseMemUsage(t *testing.T) {
	got := parseMemUsage("agentbox-machine-a-1\t312.4MiB / 4GiB\n/agentbox-machine-b-2\t1.2GiB / 1.5GiB\nnonsense\n")
	if got["agentbox-machine-a-1"] != "312.4MiB" || got["agentbox-machine-b-2"] != "1.2GiB" || len(got) != 2 {
		t.Errorf("%v", got)
	}
}
