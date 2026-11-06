//go:build integration

package incus_test

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"agentbox/internal/incus"
)

// A command whose context ends is killed in the machine, not left running:
// left, a command given up on went on holding the machine's CPU and memory,
// and each retry of it added another. Any small image will do (LAB_IMAGE,
// images:debian/13 by default).
func TestExecGivenUpOnIsKilled(t *testing.T) {
	ctx := context.Background()
	api := incus.Client{}
	if err := api.Ping(ctx); err != nil {
		t.Skipf("no Incus: %v", err)
	}
	const inst = "ab-it-execkill"
	_ = api.Delete(ctx, inst)
	t.Cleanup(func() { _ = api.Delete(context.Background(), inst) })
	if out, err := exec.Command("incus", "launch", "images:debian/13", inst).CombinedOutput(); err != nil {
		t.Skipf("no image to launch: %v: %s", err, out)
	}
	if _, err := api.WaitReady(ctx, inst, time.Minute); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := api.Exec(short, inst, "sleep", "4321")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Exec = %v, want the deadline", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, _ := api.Exec(ctx, inst, "sh", "-c", "pgrep -f 'sleep 4321' || true")
		if strings.TrimSpace(out) == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the command given up on still runs: pid %s", strings.TrimSpace(out))
		}
		time.Sleep(200 * time.Millisecond)
	}
}
