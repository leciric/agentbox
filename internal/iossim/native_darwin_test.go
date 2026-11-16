package iossim

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestSimhostCompiles compiles simhost.m with the Mac's Xcode, warnings as
// errors: the macOS CI job runs it, where Linux can only check the Go side.
// It can't run it there, with no simulator booted.
func TestSimhostCompiles(t *testing.T) {
	if _, err := exec.LookPath("xcrun"); err != nil {
		t.Skip("no Xcode")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "simhost.m")
	if err := os.WriteFile(src, simhostSource, 0o600); err != nil {
		t.Fatal(err)
	}
	args := append(SimhostArgs(src, filepath.Join(dir, "simhost")), "-Werror")
	if out, err := exec.Command("xcrun", args...).CombinedOutput(); err != nil {
		t.Fatalf("xcrun %v: %v\n%s", args, err, out)
	}
	// With no simulator by that UDID it says so, as an event, and exits.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, _ := exec.CommandContext(ctx, filepath.Join(dir, "simhost"), testUDID).Output()
	if len(out) < 5 || out[4] != kindEvent {
		t.Errorf("simhost for a missing simulator wrote %q", out)
	}
}
