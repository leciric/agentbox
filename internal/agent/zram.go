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
	"sort"
	"strconv"
	"strings"
	"time"
)

// Compressed swap in memory for AgentBox's VM. Memory pressure only shows
// (PSI) while the kernel has something to reclaim: with no swap, a VM whose
// agents' tests hold more than it has goes straight to killing one, and the
// daemon never gets to make them wait or pause the newest (package
// pressure). And a paused run's memory has nowhere to go but swap. The VM has
// no swap unless `agentbox vm swap on` made a swapfile, so the daemon gives it
// a zram device as it starts: half the VM's memory, compressed with zstd,
// used before any swapfile. It costs nothing until something is swapped to
// it, and then less than it holds.
//
// Half, not a quarter: on 2026-10-23 a quarter (4.6 GB of an 18 GB VM) was
// full when two type checks ran the VM short, so the machines frozen for
// memory kept their ~7 GB in RAM and freezing them freed nothing. zstd
// compressed those pages about 4:1, so half the VM's memory as zram costs
// about an eighth of it once full.

// ZramShare is the part of the VM's memory its zram swap may hold: 1/ZramShare.
const ZramShare = 2

// zramPriority is above a swapfile's, which swapon gives a negative one.
const zramPriority = "100"

// zramHost is what setting up zram reads and runs, as root where it must:
// the system's own, or a test's.
type zramHost struct {
	root string // where /proc and /sys are, "/" but in a test
	// write writes a file as root; run runs a command as root, and output
	// does and answers what it printed.
	write  func(path, value string) error
	run    func(name string, args ...string) error
	output func(name string, args ...string) ([]byte, error)
}

// EnsureZram gives the VM size bytes of zram swap, unless it has that much
// already, and says what it did: a device of size, or, when the VM has a
// smaller one (made by an earlier daemon, at a quarter), another for the
// rest, as a zram device in use can't be resized. It fails when the kernel
// has no zram module.
func EnsureZram(size int64) (string, error) {
	return ensureZram(zramHost{root: "/", write: writeCgroup, run: sudoRun, output: sudoOutput}, size)
}

func ensureZram(h zramHost, size int64) (string, error) {
	path := func(p string) string { return filepath.Join(h.root, p) }
	swaps, err := os.ReadFile(path("proc/swaps"))
	if err != nil {
		return "", err
	}
	var have int64
	var devs []string
	for _, line := range strings.Split(string(swaps), "\n")[1:] {
		if f := strings.Fields(line); len(f) > 2 && strings.HasPrefix(f[0], "/dev/zram") {
			kib, _ := strconv.ParseInt(f[2], 10, 64)
			have += kib << 10
			devs = append(devs, f[0])
		}
	}
	// What the kernel reports is a page or so under what was asked for.
	if len(devs) > 0 && have >= size-size/16 {
		return "the VM has zram swap already (" + strings.Join(devs, ", ") + ")", nil
	}
	size -= have
	if size < 64<<20 {
		return "", fmt.Errorf("a zram device of %d bytes is too small to be worth it", size)
	}
	if _, err := os.Stat(path("sys/class/zram-control")); errors.Is(err, fs.ErrNotExist) {
		if err := h.run("modprobe", "zram", "num_devices=1"); err != nil {
			return "", fmt.Errorf("the VM's kernel has no zram: %w", err)
		}
	}
	dev, err := freeZram(h)
	if err != nil {
		return "", err
	}
	block := path("sys/block/" + dev)
	// zstd compresses best; a kernel without it keeps its default.
	_ = h.write(filepath.Join(block, "comp_algorithm"), "zstd")
	if err := h.write(filepath.Join(block, "disksize"), strconv.FormatInt(size, 10)); err != nil {
		return "", fmt.Errorf("sizing /dev/%s: %w", dev, err)
	}
	if err := h.run("mkswap", "/dev/"+dev); err != nil {
		return "", err
	}
	if err := h.run("swapon", "-p", zramPriority, "/dev/"+dev); err != nil {
		return "", err
	}
	if len(devs) > 0 {
		return fmt.Sprintf("gave the VM %s more zram swap (/dev/%s, beside %s)", HumanBytes(size), dev, strings.Join(devs, ", ")), nil
	}
	return fmt.Sprintf("gave the VM %s of zram swap (/dev/%s)", HumanBytes(size), dev), nil
}

// freeZram is a zram device not set up yet: one the module made, else a new
// one (zram-control's hot_add).
func freeZram(h zramHost) (string, error) {
	entries, err := os.ReadDir(filepath.Join(h.root, "sys/block"))
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "zram") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(h.root, "sys/block", name, "disksize"))
		if err == nil && strings.TrimSpace(string(b)) == "0" {
			return name, nil
		}
	}
	out, err := h.output("cat", "/sys/class/zram-control/hot_add")
	if err != nil {
		return "", fmt.Errorf("adding a zram device: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return "", fmt.Errorf("adding a zram device: it answered %q", out)
	}
	return "zram" + strconv.Itoa(n), nil
}

func sudoRun(name string, args ...string) error {
	_, err := sudoOutput(name, args...)
	return err
}

func sudoOutput(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
