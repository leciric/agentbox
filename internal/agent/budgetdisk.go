package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The shared budget's disk: agents give way to the host's own apps on the
// disk, and never write faster than a cheap SSD keeps up with.
//
// Memory and CPU budgets don't cover it. At the budget, the kernel keeps
// evicting the agents' file cache and they read it back from the SSD at about
// 2 GB/s; with no memory pressure at all, a plain fio (random reads and
// writes, direct, 4 jobs at queue depth 32) does the same. Either way the
// host's own IO queues behind theirs — measured on the user's host, IO "full"
// pressure at 35–50% while the browser and the AgentBox app froze — and a
// budget NVMe that has been written to for long enough runs out of its SLC
// cache and stays 100% busy at a twentieth of its rated writes (the host's
// Kingston NV3: 4 GB/s rated, about 135 MB/s once the cache was gone).
//
// The NVMe's scheduler is none, so io.weight on its own does nothing: it is
// BFQ's knob, and nothing reads it without BFQ or io.cost. So the budget uses
// two controls:
//
//   - io.cost (blk-iocost) on the physical disks, with a latency QoS: the
//     kernel measures each disk's completion latency and, when it misses the
//     target, lowers the rate IO is issued at, so the host's latency holds
//     however much the agents queue — including when the disk itself falls off
//     a cliff, which is what iocost's min vrate is for. Within that rate, IO is
//     shared by io.weight, and the budget's cgroup gets a low one: while the
//     host needs the disk, agents get a small share; while it doesn't, they
//     get all of it. It works with any scheduler, none included.
//   - io.max's wbps on the devices the filesystems sit on, as a ceiling on the
//     agents' sustained writes. io.cost is work-conserving: with the host idle,
//     agents may write at full speed, and minutes of that is what exhausts a
//     budget drive's cache for everyone, long after the agents stop.
//
// io.latency was the other candidate. It protects a cgroup with a latency
// target by throttling its siblings, so it would have to be set on systemd's
// own user.slice and system.slice, not on AgentBox's cgroup, and it only
// shrinks the others' queue depth, down to 1, which doesn't help once the disk
// itself is slow. io.max alone, for reads too, would be a bandwidth figure
// guessed per disk and held whether the host needs the disk or not. A hard cap
// is also one the rest of the host can end up waiting behind — sync(2), or
// btrfs flushing delalloc to find space, waits for the agents' dirty pages at
// the agents' pace — so it's used only for what io.cost can't do, sustained
// writes, and it's a setting.
//
// With dm-crypt or LVM in between (the user's root is dm-0, LUKS, over
// nvme0n1), io.cost has to be on the NVMe itself — it needs a request queue,
// which a device-mapper device doesn't have — and the IO still counts against
// the agents: dm-crypt carries the cgroup over to what it sends below, as
// io.stat's lines for both devices show. io.max works at the bio level, so it
// goes on dm-0, where the agents' IO arrives: 50 MiB/s set there held a read
// through LUKS to exactly that on the user's host.
//
// io.cost.qos is a file of the root cgroup, and io.cost applies to everyone
// on the disk, so `agentbox host budget` hands it to the daemon's user with the
// budget's io.weight and io.max. The daemon turns io.cost on for the budget's
// disks while the budget is on, and off again with it.

// Where the disks are read from. Variables for tests.
var (
	sysBlock    = "/sys/class/block"
	mountInfo   = "/proc/self/mountinfo"
	procSwaps   = "/proc/swaps"
	costQoSFile = filepath.Join(cgroupRoot, "io.cost.qos")
)

// defaultDiskWeight is the budget's io.weight when nobody chose one: against
// the 100 every other cgroup has, agents get about a tenth of the disk while
// the host's own apps want it.
const defaultDiskWeight = 10

// Disk is a block device the budget controls.
type Disk struct {
	Name string // as in /sys/class/block: nvme0n1, dm-0, sda
	Dev  string // major:minor, how cgroup files name it
	// Kind is "NVMe", "SSD" or "hard disk"; "" for a device-mapper or other
	// virtual device, which is only ever a Top.
	Kind  string
	Model string
	// Request is whether it has a request queue, and so can take io.cost; BFQ
	// is whether its scheduler is BFQ, which reads io.weight itself.
	Request bool
	BFQ     bool
}

// Disks are the block devices under the host's filesystems and swap.
type Disks struct {
	// Top are the devices the filesystems are on — dm-0 for LUKS — where
	// io.max goes. Physical are the disks under them, where io.cost goes.
	Top      []Disk
	Physical []Disk
	// Root is the physical disk under /, or the first one: what the defaults
	// and Settings talk about.
	Root *Disk
}

// ReadDisks finds the disks under every mounted block filesystem and swap
// partition. Loop devices are left out: what they read and write lands on the
// filesystem they're a file in, and so on one of the others.
func ReadDisks() Disks {
	var d Disks
	seenTop, seenPhys := map[string]bool{}, map[string]bool{}
	rootTop := ""
	for _, m := range blockSources() {
		name := blockName(m.source)
		if name == "" {
			continue
		}
		top := wholeDisk(name)
		if skipDisk(top) {
			continue
		}
		if m.root && rootTop == "" {
			rootTop = top
		}
		if seenTop[top] {
			continue
		}
		disk, ok := readDisk(top)
		if !ok {
			continue
		}
		seenTop[top] = true
		d.Top = append(d.Top, disk)
		for _, p := range physicalDisks(top, 0) {
			if !seenPhys[p.Name] {
				seenPhys[p.Name] = true
				d.Physical = append(d.Physical, p)
			}
		}
	}
	if rootTop != "" {
		d.Root = firstPhysical(d.Physical, physicalDisks(rootTop, 0))
	}
	if d.Root == nil && len(d.Physical) > 0 {
		d.Root = &d.Physical[0]
	}
	return d
}

// firstPhysical is the entry of all for the first of these, so Root points
// into Physical.
func firstPhysical(all, these []Disk) *Disk {
	if len(these) == 0 {
		return nil
	}
	for i := range all {
		if all[i].Name == these[0].Name {
			return &all[i]
		}
	}
	return nil
}

type blockSource struct {
	source string
	root   bool // mounted on /
}

// blockSources are the device paths of the mounted filesystems, from
// mountinfo, and of the swap partitions.
func blockSources() []blockSource {
	var out []blockSource
	if b, err := os.ReadFile(mountInfo); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			pre, post, ok := strings.Cut(line, " - ")
			if !ok {
				continue
			}
			fields, rest := strings.Fields(pre), strings.Fields(post)
			if len(fields) < 5 || len(rest) < 2 || !strings.HasPrefix(rest[1], "/") {
				continue
			}
			out = append(out, blockSource{source: rest[1], root: fields[4] == "/"})
		}
	}
	if b, err := os.ReadFile(procSwaps); err == nil {
		for _, line := range strings.Split(string(b), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[1] == "partition" {
				out = append(out, blockSource{source: fields[0]})
			}
		}
	}
	return out
}

// blockName is the /sys/class/block name of a device path, /dev/mapper/root
// for one, or "" when it isn't a block device.
func blockName(source string) string {
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return ""
	}
	name := filepath.Base(resolved)
	if _, err := os.Stat(filepath.Join(sysBlock, name, "dev")); err != nil {
		return ""
	}
	return name
}

// wholeDisk is the disk a partition is on, or name itself: cgroups take
// whole devices only.
func wholeDisk(name string) string {
	if _, err := os.Stat(filepath.Join(sysBlock, name, "partition")); err != nil {
		return name
	}
	dir, err := filepath.EvalSymlinks(filepath.Join(sysBlock, name))
	if err != nil {
		return name
	}
	return filepath.Base(filepath.Dir(dir))
}

func skipDisk(name string) bool {
	for _, prefix := range []string{"loop", "zram", "ram", "sr", "fd"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// physicalDisks are the disks at the bottom of a device's stack: itself when
// nothing is below it, and the disks under each of its slaves otherwise.
func physicalDisks(name string, depth int) []Disk {
	slaves, _ := os.ReadDir(filepath.Join(sysBlock, name, "slaves"))
	if len(slaves) == 0 || depth > 8 {
		d, ok := readDisk(name)
		if !ok || d.Kind == "" {
			return nil
		}
		return []Disk{d}
	}
	var out []Disk
	for _, s := range slaves {
		out = append(out, physicalDisks(wholeDisk(s.Name()), depth+1)...)
	}
	return out
}

func readDisk(name string) (Disk, bool) {
	dir := filepath.Join(sysBlock, name)
	dev, err := os.ReadFile(filepath.Join(dir, "dev"))
	if err != nil {
		return Disk{}, false
	}
	d := Disk{Name: name, Dev: strings.TrimSpace(string(dev))}
	if sched, err := os.ReadFile(filepath.Join(dir, "queue", "scheduler")); err == nil {
		d.Request = true
		d.BFQ = strings.Contains(string(sched), "[bfq]")
	}
	model, _ := os.ReadFile(filepath.Join(dir, "device", "model"))
	d.Model = strings.TrimSpace(string(model))
	rotational, _ := os.ReadFile(filepath.Join(dir, "queue", "rotational"))
	switch {
	case strings.HasPrefix(name, "dm-") || strings.HasPrefix(name, "md") || !d.Request:
		// A layer over other disks: what it's like is theirs.
	case strings.TrimSpace(string(rotational)) == "1":
		d.Kind = "hard disk"
	case strings.HasPrefix(name, "nvme"):
		d.Kind = "NVMe"
	default:
		d.Kind = "SSD"
	}
	return d, true
}

// suggestDiskWrite is the agents' write ceiling for a disk: half of what a
// budget drive of its kind still writes once its cache is full. sysfs says
// nothing about that — the only speed it has, an NVMe's PCIe link, is what the
// host's NV3 is rated near (7.9 GB/s link, 4 GB/s rated writes) and 30 times
// what it wrote once its SLC cache ran out. Half of that rate leaves the
// disk able to keep up with agents writing flat out and still serve the host.
func suggestDiskWrite(d *Disk) string {
	if d != nil && d.Kind == "hard disk" {
		return "32MiB"
	}
	return "64MiB"
}

// diskQoS is the io.cost.qos a disk gets while the budget is on: the latency
// its 95th percentile of reads and writes must stay under, and how far the
// kernel may slow everyone down to hold it. The floor is 5% of iocost's model
// of the disk: a budget SSD past its cache falls to about a thirtieth of its
// rated writes, and a floor above that would leave the kernel still issuing
// faster than the disk completes, with every app queued behind it. A hard
// disk's targets are the kernel documentation's own example.
func diskQoS(d Disk) string {
	rlat, wlat := 5000, 10000 // µs
	if d.Kind == "hard disk" {
		rlat, wlat = 75000, 150000
	}
	return fmt.Sprintf("enable=1 ctrl=user rpct=95.00 rlat=%d wpct=95.00 wlat=%d min=5.00 max=150.00", rlat, wlat)
}

// ErrDiskNotReady is BudgetDiskReady's answer when the files the disk's part
// of the budget is written to aren't there, or aren't this user's.
var ErrDiskNotReady = errors.New("the shared budget's disk controls aren't set up")

// BudgetDiskReady checks that the files the budget's disk controls are written
// to are this user's: the budget's io.weight and io.max, and the root's
// io.cost.qos. They aren't part of BudgetReady — a budget set up before they
// were keeps its memory and CPU until Set up runs again.
func BudgetDiskReady() error {
	for _, path := range []string{filepath.Join(BudgetDir, "io.weight"), filepath.Join(BudgetDir, "io.max"), costQoSFile} {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%w: there is no %s, so the kernel's io controller isn't enabled there, or it has no io.cost", ErrDiskNotReady, path)
			}
			return fmt.Errorf("%w: %s isn't yours to write", ErrDiskNotReady, path)
		}
		_ = f.Close()
	}
	return nil
}

// applyBudgetDisk writes the disk's part of the budget: io.weight and io.max
// in the budget's cgroup, and io.cost.qos for the disks under it. Off, each is
// put back: weight 100, no ceiling, io.cost off. Nothing is written until all
// three files are this user's (BudgetDiskReady), so a budget set up before
// they were keeps working without them.
func applyBudgetDisk(on bool, b Budget, disks Disks) error {
	if BudgetDiskReady() != nil {
		return nil
	}
	var errs []error
	weight := "default 100"
	if on {
		weight = "default " + strconv.Itoa(b.DiskWeight)
	}
	if err := writeBudgetFile("io.weight", weight); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, applyIOMax(on, b, disks.Top)...)
	errs = append(errs, applyCostQoS(on, disks.Physical)...)
	return errors.Join(errs...)
}

// applyIOMax sets the write ceiling on each device the filesystems are on,
// and lifts it from any other device io.max still names — one unmounted since,
// or every one when the budget is off. The kernel takes one device a write.
func applyIOMax(on bool, b Budget, tops []Disk) []error {
	path := filepath.Join(BudgetDir, "io.max")
	have := nestedKeyed(readString(path))
	want := map[string]string{}
	if wbps := sizeOf(b.DiskWrite); on && wbps > 0 {
		for _, d := range tops {
			want[d.Dev] = strconv.FormatInt(wbps, 10)
		}
	}
	var errs []error
	for _, dev := range sortedKeys(have, want) {
		wbps, capped := want[dev]
		if !capped {
			wbps = "max"
		}
		if cur, ok := have[dev]; (ok && cur["wbps"] == wbps && cur["rbps"] == "max") || (!ok && !capped) {
			continue
		}
		line := dev + " rbps=max wbps=" + wbps + " riops=max wiops=max"
		if err := os.WriteFile(path, []byte(line), 0); err != nil {
			errs = append(errs, fmt.Errorf("setting the shared budget's io.max for %s: %w", dev, err))
		}
	}
	return errs
}

// applyCostQoS turns io.cost on for the budget's disks with diskQoS, or off.
// A disk on BFQ is left alone: BFQ reads io.weight itself.
func applyCostQoS(on bool, disks []Disk) []error {
	have := nestedKeyed(readString(costQoSFile))
	var errs []error
	for _, d := range disks {
		if !d.Request || d.BFQ {
			continue
		}
		want := "enable=0"
		if on {
			want = diskQoS(d)
		}
		if sameQoS(have[d.Dev], want) {
			continue
		}
		if err := os.WriteFile(costQoSFile, []byte(d.Dev+" "+want), 0); err != nil {
			errs = append(errs, fmt.Errorf("setting io.cost.qos for %s (%s): %w", d.Name, d.Dev, err))
		}
	}
	return errs
}

// sameQoS says whether io.cost.qos's line for a disk already says what want
// does. The kernel reads it back with every key, so only want's are compared,
// and numbers as numbers: it prints 95.00 for 95. A disk io.cost has never
// been on for isn't listed at all, which is the same as enable=0.
func sameQoS(have map[string]string, want string) bool {
	if have == nil {
		return want == "enable=0"
	}
	for _, kv := range strings.Fields(want) {
		k, v, _ := strings.Cut(kv, "=")
		h, ok := have[k]
		if !ok {
			return false
		}
		if h != v {
			hf, err1 := strconv.ParseFloat(h, 64)
			vf, err2 := strconv.ParseFloat(v, 64)
			if err1 != nil || err2 != nil || hf != vf {
				return false
			}
		}
		if k == "enable" && v == "0" {
			return true // off: the rest doesn't matter
		}
	}
	return true
}

// nestedKeyed reads a nested-keyed cgroup file, one device a line:
//
//	253:0 rbps=max wbps=67108864 riops=max wiops=max
func nestedKeyed(file string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, line := range strings.Split(file, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.Contains(fields[0], ":") {
			continue
		}
		kv := map[string]string{}
		for _, f := range fields[1:] {
			if k, v, ok := strings.Cut(f, "="); ok {
				kv[k] = v
			}
		}
		out[fields[0]] = kv
	}
	return out
}

func readString(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

func sortedKeys[V1, V2 any](a map[string]V1, b map[string]V2) []string {
	seen := map[string]bool{}
	var out []string
	for k := range a {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for k := range b {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// BudgetDiskCeiling is the write ceiling io.max holds the budget to right now,
// in bytes a second: the lowest over its devices, or 0 for none.
func BudgetDiskCeiling() int64 {
	var lowest int64
	for _, kv := range nestedKeyed(readString(filepath.Join(BudgetDir, "io.max"))) {
		if n, err := strconv.ParseInt(kv["wbps"], 10, 64); err == nil && (lowest == 0 || n < lowest) {
			lowest = n
		}
	}
	return lowest
}

// Describe names a disk the way Settings does: "NVMe nvme0n1 (KINGSTON
// SNV3S1000G)".
func (d Disk) Describe() string {
	s := d.Name
	if d.Kind != "" {
		s = d.Kind + " " + s
	}
	if d.Model != "" {
		s += " (" + d.Model + ")"
	}
	return s
}
