package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AgentBox's VM has no swap (decided 19 Nov 2026). Earlier daemons gave it a
// zram device as they started, half the VM's memory compressed in RAM, so
// that memory pressure showed before the kernel killed and a paused run's
// memory had somewhere to go. With agents' builds and tests filling it, it
// did the opposite of what the user needs: on 19 Nov 2026 a VM of 10 GiB had
// its 7 GiB of zram full, IO pressure at 97% and a load of 65 on 8 vCPUs, and
// the daemon, Incus and the app's every request waited behind agents that
// were never going to finish. With no swap, an agent that needs more memory
// than there is has a process killed at once, by the kernel (its own
// memory.max, every agent's together, or the VM's), and is told
// (daemon/pressure.go's tellKilled); everything else carries on.
//
// So the daemon takes away any zram swap as it starts (DropZram): a VM that
// an earlier daemon gave some keeps it until then, as a zram device lives
// until the VM stops. A swapfile from `agentbox vm swap on` is the user's own
// choice and stays.

// zramHost is what taking zram away reads and runs, as root where it must:
// the system's own, or a test's.
type zramHost struct {
	root string // where /proc and /sys are, "/" but in a test
	// write writes a file as root; run runs a command as root.
	write func(path, value string) error
	run   func(name string, args ...string) error
}

// DropZram turns off every zram swap device the VM has and frees its memory,
// and says what it did: "" when there was none. Turning a device off brings
// what it holds back into RAM, which takes as long as it takes to decompress
// it, and fails (leaving it on) when it doesn't fit.
func DropZram() (string, error) {
	return dropZram(zramHost{root: "/", write: writeCgroup, run: sudoRun})
}

func dropZram(h zramHost) (string, error) {
	swaps, err := os.ReadFile(filepath.Join(h.root, "proc/swaps"))
	if err != nil {
		return "", err
	}
	var dropped []string
	var errs []string
	for _, line := range strings.Split(string(swaps), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 4 || !strings.HasPrefix(f[0], "/dev/zram") {
			continue
		}
		dev := f[0]
		if err := h.run("swapoff", dev); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		// Reset frees the memory it held, and its size, so nothing turns
		// it on again by mistake.
		_ = h.write(filepath.Join(h.root, "sys/block", filepath.Base(dev), "reset"), "1")
		dropped = append(dropped, dev)
	}
	var did string
	if len(dropped) > 0 {
		did = "turned off the VM's zram swap (" + strings.Join(dropped, ", ") + "): it has no swap now"
	}
	if len(errs) > 0 {
		return did, fmt.Errorf("turning off zram swap: %s", strings.Join(errs, "; "))
	}
	return did, nil
}

func sudoRun(name string, args ...string) error {
	_, err := sudoOutput(name, args...)
	return err
}

func sudoOutput(name string, args ...string) ([]byte, error) {
	// swapoff of a full device decompresses all of it, which can take
	// minutes on a VM short of memory.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", append([]string{"-n", name}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
