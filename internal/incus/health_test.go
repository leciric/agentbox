package incus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// While Health says Incus isn't answering, every call fails at once, without
// asking it, and says why; Probe still asks.
func TestHealthFailsCallsAtOnce(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls")
	c := fakeIncus(t, `echo "$*" >> `+calls+`
case "$1" in list) echo '[]' ;; esac`)
	c.Health = new(Health)
	ctx := context.Background()

	c.Health.SetNotAnswering("it hasn't answered since 02:41:54")
	for name, call := range map[string]func() error{
		"Ping":      func() error { return c.Ping(ctx) },
		"Instances": func() error { _, err := c.Instances(ctx); return err },
		"Start":     func() error { return c.Start(ctx, "ab-agent-01") },
	} {
		err := call()
		if !errors.Is(err, ErrNotAnswering) || !strings.Contains(err.Error(), "since 02:41:54") {
			t.Errorf("%s = %v, want ErrNotAnswering and why", name, err)
		}
	}
	if b, _ := os.ReadFile(calls); len(b) != 0 {
		t.Errorf("Incus was asked: %s", b)
	}
	if err := c.Probe(ctx); err != nil {
		t.Errorf("Probe = %v", err)
	}

	c.Health.SetNotAnswering("")
	if _, err := c.Instances(ctx); err != nil {
		t.Errorf("after the mark is cleared: %v", err)
	}
	// A Client without Health is never gated.
	if err := (Client{Bin: c.Bin}).Ping(ctx); err != nil {
		t.Error(err)
	}
}
