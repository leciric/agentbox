package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/hostos"
)

// SetMemoryHigh sets an agent's machine's memory.high (MemoryHighs), 0 for
// none ("max"). It writes only when the value changes, and does nothing for a
// machine whose cgroup isn't there: stopped, or not on cgroup2.
//
// The cgroup is root's, made by Incus as the machine starts. In AgentBox's VM
// the daemon may use sudo (as incuswatch.go does), so the write goes through
// `sudo -n tee` when the daemon can't make it itself. Incus has no key for
// memory.high: limits.memory sets memory.max, which kills rather than slows,
// and raw.lxc only applies when a machine starts.
func SetMemoryHigh(instance string, high int64) error {
	return setMemoryHigh(cgroupRoot, instance, high, writeCgroup)
}

func setMemoryHigh(root, instance string, high int64, write func(path, value string) error) error {
	path := filepath.Join(agentCgroup(root, instance), "memory.high")
	have, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	want := "max"
	if high > 0 {
		// The kernel keeps memory.high in whole pages.
		want = strconv.FormatInt(high/4096*4096, 10)
	}
	if strings.TrimSpace(string(have)) == want {
		return nil
	}
	return write(path, want)
}

// writeCgroup writes a cgroup file, through sudo when it isn't the daemon's
// and the daemon runs in AgentBox's VM.
func writeCgroup(path, value string) error {
	err := os.WriteFile(path, []byte(value), 0)
	if err == nil || !errors.Is(err, fs.ErrPermission) || !hostos.InVM() {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", "-n", "tee", path)
	cmd.Stdin = strings.NewReader(value)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo tee %s: %w: %s", path, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
