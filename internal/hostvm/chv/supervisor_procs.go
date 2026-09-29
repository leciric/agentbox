package chv

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// child is one of the programs the supervisor runs the VM with.
type child struct {
	name string
	cmd  *exec.Cmd
	done chan struct{} // closed once it has exited
	err  error         // how it exited, once done is closed
}

// startChild starts a program whose output goes to log, a line at a time,
// each prefixed with its name. It dies with the supervisor (childAttr).
func startChild(name, path string, args []string, log io.Writer) (*child, error) {
	cmd := exec.Command(path, args...)
	w := &prefixWriter{prefix: name + ": ", w: log}
	cmd.Stdout, cmd.Stderr = w, w
	cmd.SysProcAttr = childAttr()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", name, err)
	}
	c := &child{name: name, cmd: cmd, done: make(chan struct{})}
	go func() {
		c.err = cmd.Wait()
		w.flush()
		close(c.done)
	}()
	return c, nil
}

func (c *child) exited() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *child) pid() int { return c.cmd.Process.Pid }

// stop asks it to end with SIGTERM and kills it if it hasn't after grace.
func (c *child) stop(grace time.Duration) {
	if c == nil || c.exited() {
		return
	}
	_ = c.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-c.done:
	case <-time.After(grace):
		_ = c.cmd.Process.Kill()
		<-c.done
	}
}

// resident is what the process holds of the host's memory: its anonymous
// and shared pages (the VM's memory is a shared memfd, shared=on), not the
// program's own file-backed pages.
func resident(pid int) int64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0
	}
	var total int64
	for line := range strings.Lines(string(b)) {
		f := strings.Fields(line)
		if len(f) >= 2 && (f[0] == "RssAnon:" || f[0] == "RssShmem:") {
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			total += kb << 10
		}
	}
	return total
}

// prefixWriter writes whole lines to w, each with prefix before it.
type prefixWriter struct {
	prefix string
	w      io.Writer
	mu     sync.Mutex
	buf    []byte
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		_, _ = fmt.Fprintf(p.w, "%s%s\n", p.prefix, p.buf[:i])
		p.buf = p.buf[i+1:]
	}
	if len(p.buf) > 64<<10 {
		_, _ = fmt.Fprintf(p.w, "%s%s\n", p.prefix, p.buf)
		p.buf = nil
	}
	return len(b), nil
}

func (p *prefixWriter) flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.buf) > 0 {
		_, _ = fmt.Fprintf(p.w, "%s%s\n", p.prefix, p.buf)
		p.buf = nil
	}
}

// macAddress is the VM's network card's: fixed, since Cloud Hypervisor makes
// up a new one every boot otherwise, and the guest's network configuration
// from its first boot names the card by it. Locally administered, and
// different for each VM name.
func macAddress(name string) string {
	h := sha256.Sum256([]byte("agentbox-vm:" + name))
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", h[0], h[1], h[2])
}

// virtiofsdArgs shares c.Home into the VM. sandbox is "namespace", which
// needs unprivileged user namespaces, or "none" where there are none.
func virtiofsdArgs(c Config, l Layout, sandbox string) []string {
	return []string{
		"--socket-path=" + l.FSSocket(),
		"--shared-dir=" + c.Home,
		"--sandbox=" + sandbox,
		"--cache=auto",
		"--inode-file-handles=never",
		"--xattr",
	}
}

// passtArgs is passt, as the VM's network card (vhost-user): the VM's
// traffic leaves as the user's own sockets, and its address, gateway and DNS
// are the same whatever network the host is on. No ports are forwarded to the
// VM: what the host reaches in it goes over vsock.
func passtArgs(l Layout) []string {
	args := []string{
		"--vhost-user", "--socket", l.PasstSocket(),
		"--foreground",
		"--address", GuestAddr, "--netmask", "24",
		"--gateway", GuestGateway,
		"--dns-forward", GuestDNS, "--dns", GuestDNS,
		"--tcp-ports", "none", "--udp-ports", "none",
	}
	// passt forwards the VM's DNS to the host's resolver, but skips one on a
	// loopback address (systemd-resolved's 127.0.0.53), which leaves the VM
	// with none: name it.
	if ns := hostNameserver("/etc/resolv.conf"); ns != "" {
		args = append(args, "--dns-host", ns)
	}
	return args
}

// hostNameserver is the first nameserver in resolv.conf, or "".
func hostNameserver(file string) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(b)) {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "nameserver" {
			return f[1]
		}
	}
	return ""
}

// memHotplugAlign is what virtio-mem's region must be a multiple of.
const memHotplugAlign = 128 << 20

// CapFor is the most memory c's VM is given, as its status says it: its
// MemoryCap in whole virtio-mem blocks.
func CapFor(c Config) int64 { return c.MemoryMin + hotplugSize(c) }

// hotplugSize is how much the VM can be given on top of c.MemoryMin.
func hotplugSize(c Config) int64 {
	n := c.MemoryCap - c.MemoryMin
	return max(n/memHotplugAlign*memHotplugAlign, 0)
}

// Room is how far a running VM can be resized without a restart: the vCPUs
// it can hotplug up to and the memory its virtio-mem region holds, on top of
// what it boots with. Both are fixed when Cloud Hypervisor starts, so the VM
// boots with room for every core and all of the host's memory, whatever its
// size: vCPUs it doesn't have cost nothing, and neither does a region it
// hasn't plugged, since the guest adds memory blocks only as they're plugged
// and the host's memfd is sparse. The memory policy keeps to the cap.
type Room struct {
	CPUs   int
	Memory int64 // the most the VM can hold in all
}

// hostCPUs is the host's cores.
var hostCPUs = runtime.NumCPU

// roomFor is the Room for c on a host with cpus cores and memory bytes.
func roomFor(c Config, cpus int, memory int64) Room {
	return Room{CPUs: max(cpus, c.CPUs, 1), Memory: max(memory, c.MemoryCap)}
}

// regionSize is the virtio-mem region for room: what it can hold on top of
// c.MemoryMin, in whole blocks.
func regionSize(c Config, room Room) int64 {
	return max((room.Memory-c.MemoryMin)/memHotplugAlign*memHotplugAlign, 0)
}

// chArgs is Cloud Hypervisor's command line for the VM.
func chArgs(c Config, l Layout, room Room) []string {
	// shared=on: vhost-user (passt) and virtiofsd map the VM's memory.
	// thp=on asks for transparent huge pages on it, which shared memory only
	// gets when asked (shmem_enabled=advise, the usual default): each 2 MiB
	// page the guest first touches is then one fault on the host instead of
	// 512. A host too fragmented to find free 2 MiB blocks falls back to
	// small pages, and fresh memory comes in at a few hundred MB/s.
	mem := fmt.Sprintf("size=%d,shared=on,thp=on", c.MemoryMin)
	if hp := regionSize(c, room); hp > 0 {
		mem += fmt.Sprintf(",hotplug_method=virtio-mem,hotplug_size=%d", hp)
	}
	var limit string
	args := []string{
		"--api-socket", "path=" + l.APISocket(),
		"--firmware", l.Bin("CLOUDHV.fd"),
		"--cpus", fmt.Sprintf("boot=%d,max=%d", max(c.CPUs, 1), room.CPUs),
		"--memory", mem,
		// Free page reporting gives the host back what the guest frees,
		// within seconds, without a resize.
		"--balloon", "size=0,free_page_reporting=on",
	}
	if c.IOLimit > 0 {
		// One bucket for both disks, refilled every second. Cloud
		// Hypervisor's limiter counts reads as well as writes.
		args = append(args, "--rate-limit-group", fmt.Sprintf("id=disks,bw_size=%d,bw_refill_time=1000", c.IOLimit))
		limit = ",rate_limit_group=disks"
	}
	disks := []string{
		"path=" + l.RootDisk() + ",image_type=raw" + limit,
		"path=" + l.PoolDisk() + ",image_type=raw,serial=agentbox-pool" + limit,
	}
	if _, err := os.Stat(l.Seed()); err == nil {
		// sparse=off: Cloud Hypervisor probes discard with fallocate, which a
		// read-only file fails, and says so on every boot.
		disks = append(disks, "path="+l.Seed()+",image_type=raw,readonly=on,sparse=off")
	}
	args = append(args, "--disk")
	args = append(args, disks...)
	args = append(args,
		"--net", "vhost_user=true,socket="+l.PasstSocket()+",vhost_mode=client,mac="+macAddress(c.Name),
	)
	if c.Home != "" {
		args = append(args, "--fs", "tag=home,socket="+l.FSSocket())
	}
	return append(args,
		"--vsock", fmt.Sprintf("cid=%d,socket=%s", GuestCID, l.VsockSocket()),
		"--serial", "file="+l.SerialLog(),
		"--console", "off",
	)
}

// tail is the last n lines of a file, for an error that says what happened.
func tail(file string, n int) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err == nil && st.Size() > 64<<10 {
		_, _ = f.Seek(-64<<10, io.SeekEnd)
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return strings.Join(lines, "\n")
}

// errLocked is a supervisor's lock that another supervisor holds.
var errLocked = errors.New("the VM already runs")

// lockHolders are the pids holding a FLOCK on a file with inode ino, from
// /proc/locks. A line is "1: FLOCK ADVISORY WRITE <pid> <major>:<minor>:<inode>
// 0 EOF"; one with "->" after its number is a waiter, not a holder. The device
// isn't compared: btrfs gives stat a subvolume's device, not the one
// /proc/locks names, so the caller checks what each pid is instead.
func lockHolders(locks string, ino uint64) []int {
	var pids []int
	suffix := ":" + strconv.FormatUint(ino, 10)
	for line := range strings.Lines(locks) {
		f := strings.Fields(line)
		if len(f) < 6 || f[1] != "FLOCK" || !strings.HasSuffix(f[5], suffix) || strings.Count(f[5], ":") != 2 {
			continue
		}
		if pid, err := strconv.Atoi(f[4]); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}
