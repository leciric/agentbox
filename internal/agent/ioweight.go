package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Agents' disk use yields to the daemon's and Incus's: every agent's machine
// gets an io.weight of AgentIOWeight against the 100 that everything else in
// the VM (system.slice with Incus, the daemon's session) has by default, so a
// build that saturates the VM's disk still leaves Incus's database writes and
// the daemon's reads served first, while a disk nobody else wants is all the
// build's. A weight is a share, never a ceiling.
//
// A weight only counts on a disk whose scheduler honours it: BFQ (its
// io.bfq.weight), or io.cost (io.weight). A Debian kernel's virtio disks
// start on mq-deadline, which honours neither, so in AgentBox's VM the daemon
// puts them on BFQ as it starts (UseBFQ). BFQ also serves small synchronous
// I/O like a database's ahead of a build's writeback by itself. Not io.cost:
// its model and latency targets have to fit the disk, which in a VM is the
// host's, and the latency QoS an earlier release gave it slowed the user's
// whole disk. Not io.max either: a ceiling on the agents' writes made btrfs
// commits wait on their throttled writeback and froze the host (legacy.go),
// and io.latency isn't in Debian's kernel.

// AgentIOWeight is an agent's machine's io.weight and io.bfq.weight, against
// the default 100 of the daemon's and Incus's cgroups: four agents writing
// together get as much of a busy disk as Incus does.
const AgentIOWeight = 25

// SetIOWeight gives an agent's machine's cgroup a default io.weight and
// io.bfq.weight of weight. It writes only what changes, and skips a file the
// kernel doesn't have (no BFQ loaded, or no io.cost) and a machine whose
// cgroup isn't there.
func SetIOWeight(instance string, weight int) error {
	return setIOWeight(agentCgroup(cgroupRoot, instance), weight, writeCgroup)
}

func setIOWeight(dir string, weight int, write func(path, value string) error) error {
	var errs []error
	for _, file := range []string{"io.weight", "io.bfq.weight"} {
		path := filepath.Join(dir, file)
		have, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if defaultWeight(string(have)) == weight {
			continue
		}
		if err := write(path, "default "+strconv.Itoa(weight)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// defaultWeight reads the default weight from an io.weight or io.bfq.weight
// file, "default 100" on its first line and a device's own on others; 0
// when it has none.
func defaultWeight(file string) int {
	for _, line := range strings.Split(file, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "default "); ok {
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			return n
		}
	}
	return 0
}

// UseBFQ puts every virtio disk of AgentBox's VM on the BFQ scheduler,
// loading it first when the kernel has it as a module, so the agents' io
// weight counts. It reports what it changed, "" when nothing needed to.
func UseBFQ() (string, error) {
	return useBFQ("/", writeCgroup, sudoRun)
}

func useBFQ(root string, write func(path, value string) error, run func(name string, args ...string) error) (string, error) {
	files, err := filepath.Glob(filepath.Join(root, "sys/block/vd*/queue/scheduler"))
	if err != nil || len(files) == 0 {
		return "", err
	}
	loaded := false
	var did []string
	var errs []error
	for _, file := range files {
		dev := filepath.Base(filepath.Dir(filepath.Dir(file)))
		have, err := os.ReadFile(file)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if strings.Contains(string(have), "[bfq]") {
			continue
		}
		if !strings.Contains(string(have), "bfq") && !loaded {
			loaded = true
			if err := run("modprobe", "bfq"); err != nil {
				return "", fmt.Errorf("the VM's kernel has no BFQ: %w", err)
			}
		}
		if err := write(file, "bfq"); err != nil {
			errs = append(errs, fmt.Errorf("putting /dev/%s on BFQ: %w", dev, err))
			continue
		}
		did = append(did, dev)
	}
	if len(did) == 0 {
		return "", errors.Join(errs...)
	}
	return "BFQ on " + strings.Join(did, ", "), errors.Join(errs...)
}
