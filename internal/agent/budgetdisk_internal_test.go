package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBlock lays out a /sys/class/block, a /dev and a mountinfo like the
// user's host: / on btrfs over LUKS (dm-0) over nvme0n1p2, plus a hard disk
// with a filesystem on sda1 and swap on sda2, and a snap's loop device.
func fakeBlock(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	class, devices, dev := filepath.Join(root, "class"), filepath.Join(root, "devices"), filepath.Join(root, "dev")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	disk := func(dir, devno, sched, rotational, model string) {
		write(filepath.Join(dir, "dev"), devno+"\n")
		if sched != "" {
			write(filepath.Join(dir, "queue", "scheduler"), sched+"\n")
			write(filepath.Join(dir, "queue", "rotational"), rotational+"\n")
		}
		if model != "" {
			write(filepath.Join(dir, "device", "model"), model+"   \n")
		}
	}
	part := func(dir, devno string) {
		write(filepath.Join(dir, "dev"), devno+"\n")
		write(filepath.Join(dir, "partition"), "1\n")
	}

	nvme := filepath.Join(devices, "pci0000:00", "nvme", "nvme0", "nvme0n1")
	disk(nvme, "259:4", "[none] mq-deadline kyber bfq", "0", "KINGSTON SNV3S1000G")
	part(filepath.Join(nvme, "nvme0n1p2"), "259:6")
	dm := filepath.Join(devices, "virtual", "block", "dm-0")
	disk(dm, "253:0", "", "", "")
	link(filepath.Join(class, "nvme0n1p2"), filepath.Join(dm, "slaves", "nvme0n1p2"))
	sda := filepath.Join(devices, "pci0000:00", "ata1", "sda")
	disk(sda, "8:0", "mq-deadline [bfq] none", "1", "WDC WD40EFRX")
	part(filepath.Join(sda, "sda1"), "8:1")
	part(filepath.Join(sda, "sda2"), "8:2")
	loop := filepath.Join(devices, "virtual", "block", "loop0")
	disk(loop, "7:0", "[none]", "0", "")
	for name, target := range map[string]string{
		"nvme0n1": nvme, "nvme0n1p2": filepath.Join(nvme, "nvme0n1p2"), "dm-0": dm,
		"sda": sda, "sda1": filepath.Join(sda, "sda1"), "sda2": filepath.Join(sda, "sda2"), "loop0": loop,
	} {
		link(target, filepath.Join(class, name))
	}
	for _, name := range []string{"dm-0", "sda1", "sda2", "loop0"} {
		write(filepath.Join(dev, name), "")
	}
	link(filepath.Join(dev, "dm-0"), filepath.Join(dev, "mapper", "root"))

	info := strings.Join([]string{
		"26 1 0:25 /@ / rw,relatime shared:1 - btrfs " + filepath.Join(dev, "mapper", "root") + " rw,compress=zstd:3",
		"27 26 0:25 /@home /home rw,relatime shared:2 - btrfs " + filepath.Join(dev, "mapper", "root") + " rw",
		"28 26 0:5 / /proc rw - proc proc rw",
		"29 26 8:1 / /data rw - ext4 " + filepath.Join(dev, "sda1") + " rw",
		"30 26 7:0 / /snap/core/1 ro - squashfs " + filepath.Join(dev, "loop0") + " ro",
		"31 26 0:40 / /var/lib/docker/overlay2/x/merged rw - overlay overlay rw",
	}, "\n")
	write(filepath.Join(root, "mountinfo"), info+"\n")
	write(filepath.Join(root, "swaps"), "Filename\tType\tSize\tUsed\tPriority\n"+filepath.Join(dev, "sda2")+"\tpartition\t100\t0\t-2\n/dev/zram0\tpartition\t100\t0\t100\n")

	oldBlock, oldInfo, oldSwaps := sysBlock, mountInfo, procSwaps
	sysBlock, mountInfo, procSwaps = class, filepath.Join(root, "mountinfo"), filepath.Join(root, "swaps")
	t.Cleanup(func() { sysBlock, mountInfo, procSwaps = oldBlock, oldInfo, oldSwaps })
}

func TestReadDisks(t *testing.T) {
	fakeBlock(t)
	d := ReadDisks()
	var tops, phys []string
	for _, x := range d.Top {
		tops = append(tops, x.Name+"="+x.Dev)
	}
	for _, x := range d.Physical {
		phys = append(phys, x.Name+"="+x.Dev+" "+x.Kind)
	}
	// io.max on the LUKS device, where the agents' IO arrives, and on the
	// whole disk under the partitions, since cgroups take no partitions; the
	// snap's loop device is on neither list, and sda comes once for its
	// filesystem and its swap.
	if got := strings.Join(tops, ", "); got != "dm-0=253:0, sda=8:0" {
		t.Errorf("Top = %s", got)
	}
	// io.cost on the NVMe under the LUKS device.
	if got := strings.Join(phys, ", "); got != "nvme0n1=259:4 NVMe, sda=8:0 hard disk" {
		t.Errorf("Physical = %s", got)
	}
	if d.Root == nil || d.Root.Name != "nvme0n1" || d.Root.Model != "KINGSTON SNV3S1000G" || !d.Root.Request {
		t.Fatalf("Root = %+v", d.Root)
	}
	if got := d.Root.Describe(); got != "NVMe nvme0n1 (KINGSTON SNV3S1000G)" {
		t.Errorf("Describe = %q", got)
	}
	if !d.Physical[1].BFQ {
		t.Error("sda runs BFQ, and isn't marked so")
	}
}

// fakeBudgetDisk points the budget's cgroup and the root's io.cost.qos at
// files of a temporary directory, all of them this user's.
func fakeBudgetDisk(t *testing.T) (dir, qos string) {
	t.Helper()
	dir = t.TempDir()
	qos = filepath.Join(t.TempDir(), "io.cost.qos")
	for _, path := range []string{filepath.Join(dir, "io.weight"), filepath.Join(dir, "io.max"), qos} {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldDir, oldQoS := BudgetDir, costQoSFile
	BudgetDir, costQoSFile = dir, qos
	t.Cleanup(func() { BudgetDir, costQoSFile = oldDir, oldQoS })
	return dir, qos
}

func TestApplyBudgetDisk(t *testing.T) {
	dir, qos := fakeBudgetDisk(t)
	read := func(path string) string {
		b, _ := os.ReadFile(path)
		return string(b)
	}
	nvme := Disk{Name: "nvme0n1", Dev: "259:4", Kind: "NVMe", Request: true}
	disks := Disks{
		Top:      []Disk{{Name: "dm-0", Dev: "253:0"}},
		Physical: []Disk{nvme, {Name: "sda", Dev: "8:0", Kind: "hard disk", Request: true, BFQ: true}},
	}
	b := Budget{DiskWeight: 10, DiskWrite: "64MiB"}
	if err := applyBudgetDisk(true, b, disks); err != nil {
		t.Fatal(err)
	}
	if got := read(filepath.Join(dir, "io.weight")); got != "default 10" {
		t.Errorf("io.weight = %q", got)
	}
	if got := read(filepath.Join(dir, "io.max")); got != "253:0 rbps=max wbps=67108864 riops=max wiops=max" {
		t.Errorf("io.max = %q", got)
	}
	// io.cost on the NVMe; the BFQ disk is left alone, since BFQ reads
	// io.weight itself.
	if got := read(qos); got != "259:4 enable=1 ctrl=user rpct=95.00 rlat=5000 wpct=95.00 wlat=10000 min=5.00 max=150.00" {
		t.Errorf("io.cost.qos = %q", got)
	}

	// The kernel's way of reading them back, with a newline, every key and
	// its own number formats, counts as the same: nothing is written again,
	// which would have left the line without its newline.
	kernel := map[string]string{
		filepath.Join(dir, "io.weight"): "default 10\n",
		filepath.Join(dir, "io.max"):    "253:0 rbps=max wbps=67108864 riops=max wiops=max\n",
		qos:                             "259:4 enable=1 ctrl=user rpct=95.00 rlat=5000 wpct=95.00 wlat=10000 min=5.00 max=150.00\n",
	}
	for path, body := range kernel {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := applyBudgetDisk(true, b, disks); err != nil {
		t.Fatal(err)
	}
	for path, body := range kernel {
		if got := read(path); got != body {
			t.Errorf("%s was written again: %q", filepath.Base(path), got)
		}
	}
	if got := BudgetDiskCeiling(); got != 64<<20 {
		t.Errorf("BudgetDiskCeiling = %d", got)
	}

	// A device io.max still names, unmounted since, has its ceiling lifted.
	if err := os.WriteFile(filepath.Join(dir, "io.max"), []byte("253:0 rbps=max wbps=67108864 riops=max wiops=max\n8:0 rbps=max wbps=67108864 riops=max wiops=max\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyBudgetDisk(true, b, disks); err != nil {
		t.Fatal(err)
	}
	if got := read(filepath.Join(dir, "io.max")); got != "8:0 rbps=max wbps=max riops=max wiops=max" {
		t.Errorf("io.max after an unmount = %q, want only 8:0's lifted", got)
	}

	// Off: weight back to 100, the ceiling lifted, io.cost off.
	if err := os.WriteFile(filepath.Join(dir, "io.max"), []byte(kernel[filepath.Join(dir, "io.max")]), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyBudgetDisk(false, b, disks); err != nil {
		t.Fatal(err)
	}
	if got := read(filepath.Join(dir, "io.weight")); got != "default 100" {
		t.Errorf("off: io.weight = %q", got)
	}
	if got := read(filepath.Join(dir, "io.max")); got != "253:0 rbps=max wbps=max riops=max wiops=max" {
		t.Errorf("off: io.max = %q", got)
	}
	if got := read(qos); got != "259:4 enable=0" {
		t.Errorf("off: io.cost.qos = %q", got)
	}

	// No write ceiling: "max" lifts it too.
	if err := os.WriteFile(filepath.Join(dir, "io.max"), []byte(kernel[filepath.Join(dir, "io.max")]), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyBudgetDisk(true, Budget{DiskWeight: 10, DiskWrite: "max"}, disks); err != nil {
		t.Fatal(err)
	}
	if got := read(filepath.Join(dir, "io.max")); got != "253:0 rbps=max wbps=max riops=max wiops=max" {
		t.Errorf("DiskWrite max: io.max = %q", got)
	}
	if got := BudgetDiskCeiling(); got != 0 {
		t.Errorf("BudgetDiskCeiling after a lift = %d", got)
	}
}

// A budget set up before the disk's files were handed over keeps its memory
// and CPU: the disk's part is skipped, not an error.
func TestApplyBudgetDiskWaitsForSetUp(t *testing.T) {
	dir, _ := fakeBudgetDisk(t)
	if err := os.Remove(filepath.Join(dir, "io.max")); err != nil {
		t.Fatal(err)
	}
	if err := BudgetDiskReady(); err == nil || !strings.Contains(err.Error(), "io.max") {
		t.Errorf("BudgetDiskReady = %v, want it to name io.max", err)
	}
	if err := applyBudgetDisk(true, Budget{DiskWeight: 10, DiskWrite: "64MiB"}, Disks{Top: []Disk{{Dev: "253:0"}}}); err != nil {
		t.Errorf("applyBudgetDisk = %v, want nothing done", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "io.weight")); len(b) != 0 {
		t.Errorf("io.weight was written: %q", b)
	}
}

func TestSameQoS(t *testing.T) {
	for _, c := range []struct {
		have string
		want string
		same bool
	}{
		{"", "enable=0", true},
		{"", "enable=1 rlat=5000", false},
		{"259:4 enable=0 ctrl=auto rpct=0.00 rlat=250000 wpct=0.00 wlat=250000 min=1.00 max=10000.00", "enable=0", true},
		{"259:4 enable=1 ctrl=user rpct=95.00 rlat=5000 wpct=95.00 wlat=10000 min=5.00 max=150.00", "enable=1 ctrl=user rpct=95 rlat=5000 wpct=95 wlat=10000 min=5 max=150", true},
		{"259:4 enable=1 ctrl=user rpct=95.00 rlat=5000 wpct=95.00 wlat=10000 min=5.00 max=150.00", "enable=1 ctrl=user rpct=95.00 rlat=75000", false},
	} {
		if got := sameQoS(nestedKeyed(c.have)["259:4"], c.want); got != c.same {
			t.Errorf("sameQoS(%q, %q) = %v", c.have, c.want, got)
		}
	}
}

func TestSuggestDiskWrite(t *testing.T) {
	for d, want := range map[*Disk]string{
		nil:                 "64MiB",
		{Kind: "NVMe"}:      "64MiB",
		{Kind: "SSD"}:       "64MiB",
		{Kind: "hard disk"}: "32MiB",
	} {
		if got := suggestDiskWrite(d); got != want {
			t.Errorf("suggestDiskWrite(%+v) = %q, want %q", d, got, want)
		}
	}
	h := HostResources{Memory: 30 * gib, Swap: 16 * gib, SwapKind: "zram", Cores: 16, Disk: &Disk{Kind: "hard disk"}}
	b, why := SuggestBudget(h)
	if b.DiskWrite != "32MiB" || b.DiskWeight != 10 || !strings.Contains(why, "On its hard disk,") {
		t.Errorf("SuggestBudget on a hard disk = %+v, %q", b, why)
	}
}
