package main

import (
	"bufio"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// chDriver is the prototype of the VM mode, for before the real one
// (agentbox/feat-cloud-hypervisor-vm) can run here: AgentBox's shape, one
// Cloud Hypervisor VM with Incus in it and every agent an Incus container
// inside, driven directly rather than through agentbox. What it measures is
// what the VM changes — its boot, pause and resume, what the host holds for
// it, and what the host feels while agents work in it — not AgentBox's own
// work on top, which the VM mode shares with today's.
//
// The VM is a Debian 13 cloud image booted with Cloud Hypervisor's EDK2
// firmware; cloud-init reads its seed from a small FAT disk (fat.go) and adds
// a user with a throwaway key. Its network is a tap on the host's Incus
// bridge, so the bridge's DHCP and NAT serve it as they serve today's agents.
// Its disks are raw files on the host's filesystem, opened with O_DIRECT by
// default (--ch-disk), so the guest's writes don't go through the host's page
// cache: the VM's own memory is its cache. Its memory has a balloon with free
// page reporting (--ch-balloon), which is how memory a guest frees gets back
// to the host.
type chDriver struct {
	o    *options
	r    *runner
	work string // the run's VM: disks, key, sockets, logs; deleted by cleanup
	dl   string // downloads, kept between runs

	ch, remote, firmware, image string
	bridge                      string
	goVersion, nodeVersion      string

	proc   *exec.Cmd
	exited chan struct{}
	pid    atomic.Int64 // the running VM's, for held, which the sampler calls from its own goroutine
	ip     string
	incus  bool // Incus is set up in the guest: boot waits for it
}

const (
	chVersion     = "v53.0"
	chFirmwareTag = "ch-97eeb7b09"
	debianImage   = "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.tar.xz"
	chTap         = "abbench0"
	chMAC         = "52:54:00:ab:be:01"
	chRootDisk    = "32G"
	chPoolDisk    = "96G" // sparse: it holds the agents, their builds and the sustained write
)

func newCHDriver(o *options) *chDriver {
	return &chDriver{
		o:    o,
		r:    &runner{},
		work: filepath.Join(o.cache, "chproto"),
		dl:   filepath.Join(o.cache, "dl"),
	}
}

func (d *chDriver) name() string        { return "chproto" }
func (d *chDriver) setRunner(r *runner) { d.r = r }
func (d *chDriver) ownsVM() bool        { return true }
func (d *chDriver) vm() vmOps           { return d }

func (d *chDriver) describe() map[string]any {
	return map[string]any{
		"cloudHypervisor": chVersion, "firmware": "CLOUDHV.fd " + chFirmwareTag, "image": debianImage,
		"memory": d.o.chMemory, "cpus": d.o.chCPUs, "diskOptions": d.o.chDisk, "balloon": d.o.chBalloon,
		"bridge": d.bridge, "go": d.goVersion, "node": d.nodeVersion,
		"commandLine": strings.Join(quoteAll(append([]string{d.chBin()}, d.args()...)), " "),
		"agents":      "Incus containers in the VM, on a btrfs pool on the VM's second disk, copied from a Debian 13 container with Go and Node",
	}
}

func (d *chDriver) chBin() string {
	if d.o.chBinary != "" {
		return d.o.chBinary
	}
	return filepath.Join(d.dl, "cloud-hypervisor-static-"+chVersion)
}

func (d *chDriver) remoteBin() string {
	if d.o.chBinary != "" {
		if p, err := exec.LookPath("ch-remote"); err == nil {
			return p
		}
	}
	return filepath.Join(d.dl, "ch-remote-static-"+chVersion)
}

func (d *chDriver) preflight(ctx context.Context) error {
	var problems []string
	if f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0); err != nil {
		problems = append(problems, fmt.Sprintf("/dev/kvm: %v (be in the kvm group)", err))
	} else {
		_ = f.Close()
	}
	for _, tool := range []string{"tar", "xz", "ssh", "ssh-keygen", "git", "ip"} {
		if _, err := exec.LookPath(tool); err != nil {
			problems = append(problems, tool+" isn't installed")
		}
	}
	d.bridge = d.o.chBridge
	if d.bridge == "" {
		out, err := d.r.run(ctx, "", "incus", "profile", "device", "get", "default", "eth0", "network")
		d.bridge = strings.TrimSpace(out)
		if err != nil || d.bridge == "" {
			problems = append(problems, "no --ch-bridge given, and Incus's default profile names no bridge: pass a bridge with DHCP and NAT, like incusbr0")
		}
	}
	if d.bridge != "" {
		if _, err := os.Stat("/sys/class/net/" + d.bridge + "/bridge"); err != nil {
			problems = append(problems, "bridge "+d.bridge+" isn't there")
		}
	}
	if d.running() {
		problems = append(problems, "a VM from an earlier run is still running: run "+os.Args[0]+" --cleanup --modes chproto")
	}
	if _, err := os.Stat("/sys/class/net/" + chTap); err == nil && d.madeTap() {
		problems = append(problems, "tap "+chTap+" is left from an earlier run: run "+os.Args[0]+" --cleanup --modes chproto")
	} else if err != nil {
		if _, err := d.r.run(ctx, "", "sudo", "-n", "true"); err != nil {
			problems = append(problems, fmt.Sprintf("the VM's tap needs root once, and sudo asks for a password: run scripts/bench/run.sh (it asks first), or make the tap yourself:\n  sudo ip tuntap add dev %s mode tap user %s && sudo ip link set %s master %s up", chTap, currentUser(), chTap, cmpOr(d.bridge, "incusbr0")))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	if err := checkFree(d.work, d.o.writeGiB+30); err != nil {
		return err
	}
	if mi, err := readMeminfo(); err == nil {
		if want := parseSize(d.o.chMemory); want > mi["MemAvailable"]-2<<30 {
			return fmt.Errorf("--ch-memory %s is more than the host has free (%.1f GiB available)", d.o.chMemory, gib(mi["MemAvailable"]))
		}
	}
	var err error
	if d.goVersion, err = goVersionOf(d.o.repo); err != nil {
		return err
	}
	if d.nodeVersion, err = nodeLTS(ctx); err != nil {
		return fmt.Errorf("finding Node's LTS release: %w", err)
	}
	return nil
}

func (d *chDriver) setup(ctx context.Context, m *modeRun) error {
	if err := os.MkdirAll(d.work, 0o755); err != nil {
		return err
	}
	cached := d.downloaded()
	name := "download Cloud Hypervisor " + chVersion + ", its firmware and the Debian 13 cloud image"
	if cached {
		name += " (cached from an earlier run)"
	}
	if _, err := m.setupStep(ctx, name, d.download); err != nil {
		return err
	}
	if _, err := m.setupStep(ctx, "make the VM's disks and its cloud-init seed", d.makeDisks); err != nil {
		return err
	}
	if _, err := m.setupStep(ctx, "make the VM's network (a tap on "+d.bridge+")", d.makeTap); err != nil {
		return err
	}
	if _, err := m.setupStep(ctx, "first boot, with cloud-init", func(ctx context.Context) error {
		if err := d.boot(ctx); err != nil {
			return err
		}
		_, err := d.ssh(ctx, "", "cloud-init status --wait >/dev/null; true")
		return err
	}); err != nil {
		return err
	}
	for _, s := range []struct{ name, script string }{
		{"install Incus in the VM", guestInstallIncus},
		{"set Incus up (btrfs pool on its own disk, a bridge)", guestInitIncus},
		{"make the agents' base: Debian 13 with Go " + d.goVersion + " and Node " + d.nodeVersion, guestBase(d.goVersion, d.nodeVersion)},
	} {
		if _, err := m.setupStep(ctx, s.name, func(ctx context.Context) error {
			// As an argument, not on stdin: incus launch would read the rest of
			// the script as its configuration.
			_, err := d.ssh(ctx, "", "bash -c "+shq(s.script))
			return err
		}); err != nil {
			return err
		}
	}
	d.incus = true
	_, err := m.setupStep(ctx, "copy the repository into the VM", func(ctx context.Context) error {
		bundle := filepath.Join(d.work, "repo.bundle")
		if _, err := d.r.run(ctx, "", "git", "-C", d.o.repo, "bundle", "create", "-q", bundle, "HEAD"); err != nil {
			return err
		}
		b, err := os.ReadFile(bundle)
		if err != nil {
			return err
		}
		_, err = d.ssh(ctx, string(b), "sudo tee /srv/repo.bundle >/dev/null")
		return err
	})
	return err
}

func (d *chDriver) downloaded() bool {
	for _, f := range []string{d.chBin(), d.remoteBin(), filepath.Join(d.dl, "CLOUDHV-"+chFirmwareTag+".fd"), filepath.Join(d.dl, "debian-13-genericcloud-amd64.raw")} {
		if _, err := os.Stat(f); err != nil {
			return false
		}
	}
	return true
}

func (d *chDriver) download(ctx context.Context) error {
	if err := os.MkdirAll(d.dl, 0o755); err != nil {
		return err
	}
	rel := "https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/" + chVersion + "/"
	if d.o.chBinary == "" {
		if err := fetch(ctx, rel+"cloud-hypervisor-static", d.chBin(), 0o755); err != nil {
			return err
		}
		if err := fetch(ctx, rel+"ch-remote-static", d.remoteBin(), 0o755); err != nil {
			return err
		}
	}
	d.firmware = filepath.Join(d.dl, "CLOUDHV-"+chFirmwareTag+".fd")
	if err := fetch(ctx, "https://github.com/cloud-hypervisor/edk2/releases/download/"+chFirmwareTag+"/CLOUDHV.fd", d.firmware, 0o644); err != nil {
		return err
	}
	d.image = filepath.Join(d.dl, "debian-13-genericcloud-amd64.raw")
	if _, err := os.Stat(d.image); err == nil {
		return nil
	}
	tarball := filepath.Join(d.dl, filepath.Base(debianImage))
	if err := fetch(ctx, debianImage, tarball, 0o644); err != nil {
		return err
	}
	if err := checkSHA512(ctx, tarball, strings.TrimSuffix(debianImage, filepath.Base(debianImage))+"SHA512SUMS"); err != nil {
		_ = os.Remove(tarball)
		return err
	}
	// The image is a sparse 3 GiB disk.raw in the tarball: -S keeps it sparse.
	if _, err := d.r.run(ctx, "", "tar", "-xSJf", tarball, "-C", d.dl, "disk.raw"); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(d.dl, "disk.raw"), d.image); err != nil {
		return err
	}
	return os.Remove(tarball)
}

func (d *chDriver) makeDisks(ctx context.Context) error {
	root := filepath.Join(d.work, "root.raw")
	// A reflink copy is instant on btrfs and xfs, and a plain copy elsewhere.
	if _, err := d.r.run(ctx, "", "cp", "--reflink=auto", "--sparse=always", d.image, root); err != nil {
		return err
	}
	if _, err := d.r.run(ctx, "", "truncate", "-s", chRootDisk, root); err != nil {
		return err
	}
	if _, err := d.r.run(ctx, "", "truncate", "-s", chPoolDisk, filepath.Join(d.work, "pool.raw")); err != nil {
		return err
	}
	key := filepath.Join(d.work, "key")
	_ = os.Remove(key)
	_ = os.Remove(key + ".pub")
	if _, err := d.r.run(ctx, "", "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "agentbox-bench", "-f", key); err != nil {
		return err
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		return err
	}
	seed, err := fatImage("CIDATA", map[string][]byte{
		"meta-data": []byte("instance-id: agentbox-bench-" + strconv.FormatInt(time.Now().Unix(), 10) + "\nlocal-hostname: agentbox-bench\n"),
		"user-data": []byte(guestUserData(strings.TrimSpace(string(pub)))),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d.work, "seed.img"), seed, 0o644)
}

func (d *chDriver) makeTap(ctx context.Context) error {
	if _, err := os.Stat("/sys/class/net/" + chTap); err == nil {
		d.r.logf("tap %s is already there (made by hand): using it, and leaving it", chTap)
		return nil
	}
	uid := strconv.Itoa(os.Getuid())
	for _, argv := range [][]string{
		{"sudo", "-n", "ip", "tuntap", "add", "dev", chTap, "mode", "tap", "user", uid},
		{"sudo", "-n", "ip", "link", "set", chTap, "master", d.bridge},
		{"sudo", "-n", "ip", "link", "set", chTap, "up"},
	} {
		if _, err := d.r.run(ctx, "", argv...); err != nil {
			return err
		}
		if argv[3] == "tuntap" {
			if err := os.WriteFile(filepath.Join(d.work, "tap-made-by-bench"), nil, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *chDriver) madeTap() bool {
	_, err := os.Stat(filepath.Join(d.work, "tap-made-by-bench"))
	return err == nil
}

func (d *chDriver) args() []string {
	disk := func(name, extra string) string {
		s := "path=" + filepath.Join(d.work, name) + ",image_type=raw"
		if d.o.chDisk != "" {
			s += "," + d.o.chDisk
		}
		return s + extra
	}
	a := []string{
		"--api-socket", "path=" + filepath.Join(d.work, "api.sock"),
		"--firmware", filepath.Join(d.dl, "CLOUDHV-"+chFirmwareTag+".fd"),
		"--cpus", "boot=" + strconv.Itoa(d.o.chCPUs),
		"--memory", "size=" + d.o.chMemory,
		"--disk", disk("root.raw", ""), disk("pool.raw", ""), "path=" + filepath.Join(d.work, "seed.img") + ",image_type=raw,readonly=on",
		"--net", "tap=" + chTap + ",mac=" + chMAC,
		"--serial", "file=" + filepath.Join(d.work, "serial.log"),
		"--console", "off",
	}
	if d.o.chBalloon {
		a = append(a, "--balloon", "size=0,free_page_reporting=on")
	}
	return a
}

var ipLine = regexp.MustCompile(`BENCH-IP=([0-9.]+)`)

// boot starts the VM and waits until it can make agents: its address on
// the serial console, ssh, and Incus once it's set up.
func (d *chDriver) boot(ctx context.Context) error {
	if d.running() {
		return errors.New("the VM is already running")
	}
	serial := filepath.Join(d.work, "serial.log")
	_ = os.Remove(serial)
	_ = os.Remove(filepath.Join(d.work, "api.sock"))
	logf, err := os.OpenFile(filepath.Join(d.work, "ch.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(d.chBin(), d.args()...)
	// Its own process group: Ctrl-C reaches the harness, which then shuts the
	// VM down, rather than killing the VM under a half-made cleanup.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout, cmd.Stderr = logf, logf
	d.r.logf("$ %s", strings.Join(quoteAll(cmd.Args), " "))
	if err := cmd.Start(); err != nil {
		_ = logf.Close()
		return err
	}
	d.proc, d.exited = cmd, make(chan struct{})
	_ = os.WriteFile(filepath.Join(d.work, "ch.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	exited := d.exited
	d.pid.Store(int64(cmd.Process.Pid))
	go func() { _ = cmd.Wait(); d.pid.Store(0); _ = logf.Close(); close(exited) }()

	wctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	d.ip = ""
	for d.ip == "" {
		if b, err := os.ReadFile(serial); err == nil {
			if m := ipLine.FindSubmatch(b); m != nil {
				d.ip = string(m[1])
				break
			}
		}
		select {
		case <-exited:
			return fmt.Errorf("cloud-hypervisor exited: %s", lastLines(readTail(filepath.Join(d.work, "ch.log")), 3))
		case <-wctx.Done():
			return fmt.Errorf("the VM didn't say its address on the serial console (%s)", serial)
		case <-time.After(50 * time.Millisecond):
		}
	}
	for {
		_, err := d.ssh(wctx, "", "true")
		if err == nil {
			break
		}
		if sleepCtx(wctx, 100*time.Millisecond) != nil {
			return fmt.Errorf("ssh to the VM at %s: %w", d.ip, err)
		}
	}
	if d.incus {
		_, err := d.ssh(wctx, "", "sudo incus admin waitready --timeout=120")
		return err
	}
	return nil
}

func (d *chDriver) running() bool {
	if d.exited != nil {
		select {
		case <-d.exited:
			return false
		default:
			return true
		}
	}
	// After a run that was killed: its VM's pid, if that process is still a
	// cloud-hypervisor.
	pid := d.leftoverPID()
	return pid > 0
}

func (d *chDriver) leftoverPID() int {
	b, err := os.ReadFile(filepath.Join(d.work, "ch.pid"))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid <= 0 {
		return 0
	}
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(comm)), "cloud-hyper") {
		return 0
	}
	return pid
}

// shutdown powers the guest off, as the app's "free resources" would, and
// waits for Cloud Hypervisor to exit.
func (d *chDriver) shutdown(ctx context.Context) error {
	if !d.running() {
		return nil
	}
	_, _ = d.ssh(ctx, "", "sudo systemctl poweroff")
	select {
	case <-d.exited:
		return nil
	case <-time.After(90 * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	}
	return d.kill(ctx)
}

// kill ends the VM without asking the guest: cleanup's way.
func (d *chDriver) kill(ctx context.Context) error {
	_, _ = d.r.run(ctx, "", d.remoteBin(), "--api-socket", filepath.Join(d.work, "api.sock"), "shutdown-vmm")
	pid := 0
	if d.proc != nil {
		pid = d.proc.Process.Pid
	} else {
		pid = d.leftoverPID()
	}
	deadline := time.Now().Add(10 * time.Second)
	for d.running() && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if d.running() && pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		time.Sleep(500 * time.Millisecond)
	}
	if d.running() {
		return fmt.Errorf("cloud-hypervisor (pid %d) won't exit", pid)
	}
	return nil
}

func (d *chDriver) pause(ctx context.Context) error {
	_, err := d.r.run(ctx, "", d.remoteBin(), "--api-socket", filepath.Join(d.work, "api.sock"), "pause")
	return err
}

// resume counts until Incus in the guest answers again.
func (d *chDriver) resume(ctx context.Context) error {
	if _, err := d.r.run(ctx, "", d.remoteBin(), "--api-socket", filepath.Join(d.work, "api.sock"), "resume"); err != nil {
		return err
	}
	_, err := d.ssh(ctx, "", "sudo incus list -f csv -c n >/dev/null")
	return err
}

// ssh runs command in the guest, with stdin.
func (d *chDriver) ssh(ctx context.Context, stdin, command string) (string, error) {
	if d.ip == "" {
		return "", errors.New("the VM has no address yet")
	}
	return d.r.run(ctx, stdin, "ssh", "-T", "-i", filepath.Join(d.work, "key"),
		"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR", "-o", "ConnectTimeout=5", "-o", "ServerAliveInterval=15",
		"bench@"+d.ip, command)
}

func (d *chDriver) create(ctx context.Context, agent string) error {
	_, err := d.ssh(ctx, "", fmt.Sprintf(`set -e
sudo incus copy base %[1]s
sudo incus start %[1]s
sudo incus exec %[1]s -- sh -c 'for i in $(seq 1 200); do ip -4 route get 1.1.1.1 >/dev/null 2>&1 && exit 0; sleep 0.1; done; exit 1'
sudo incus file push /srv/repo.bundle %[1]s/tmp/repo.bundle
sudo incus exec %[1]s --user 1000 --group 1000 --env HOME=/home/bench -- git -c advice.detachedHead=false clone -q /tmp/repo.bundle /home/bench/work`, agent))
	return err
}

func (d *chDriver) action(ctx context.Context, agent, action string) error {
	_, err := d.ssh(ctx, "", "sudo incus "+action+" "+agent)
	return err
}

func (d *chDriver) exec(ctx context.Context, agent, script string) error {
	_, err := d.ssh(ctx, "", fmt.Sprintf("sudo incus exec %s --user 1000 --group 1000 --cwd /home/bench/work --env HOME=/home/bench --env USER=bench -- bash -lc %s", agent, shq(script)))
	return err
}

func (d *chDriver) guestCounters(ctx context.Context) (vmCounters, error) {
	out, err := d.ssh(ctx, "", vmCountersScript)
	return parseGuestCounters(out), err
}

func (d *chDriver) dropCaches(ctx context.Context) error {
	_, err := d.ssh(ctx, "", "sync && echo 3 | sudo tee /proc/sys/vm/drop_caches >/dev/null")
	return err
}

func (d *chDriver) held() int64 {
	pid := d.pid.Load()
	if pid == 0 {
		return 0
	}
	return processRSS(strconv.FormatInt(pid, 10))
}

func (d *chDriver) cleanup(ctx context.Context) error {
	var errs []error
	if d.running() {
		if err := d.kill(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if d.madeTap() {
		if _, err := os.Stat("/sys/class/net/" + chTap); err == nil {
			if _, err := d.r.run(ctx, "", "sudo", "-n", "ip", "link", "del", chTap); err != nil {
				errs = append(errs, fmt.Errorf("%w (remove it with: sudo ip link del %s)", err, chTap))
			}
		}
		if len(errs) == 0 {
			_ = os.Remove(filepath.Join(d.work, "tap-made-by-bench"))
		}
	}
	if len(errs) == 0 {
		// The run's VM, not the downloads.
		if err := os.RemoveAll(d.work); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// fetch downloads url to path, unless it's there already.
func fetch(ctx context.Context, url, path string, mode os.FileMode) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	part := path + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(part)
		return fmt.Errorf("GET %s: %w", url, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(part, path)
}

func checkSHA512(ctx context.Context, path, sumsURL string) error {
	tmp := path + ".sums"
	defer func() { _ = os.Remove(tmp) }()
	if err := fetch(ctx, sumsURL, tmp, 0o644); err != nil {
		return err
	}
	sums, err := os.ReadFile(tmp)
	if err != nil {
		return err
	}
	want := ""
	for line := range strings.SplitSeq(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == filepath.Base(path) {
			want = f[0]
		}
	}
	if want == "" {
		return fmt.Errorf("%s isn't in %s", filepath.Base(path), sumsURL)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha512.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("%s: SHA-512 %s, want %s", path, got, want)
	}
	return nil
}

// goVersionOf is the Go the repository asks for, in go.mod.
func goVersionOf(repo string) (string, error) {
	f, err := os.Open(filepath.Join(repo, "go.mod"))
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	v := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[0] == "go" && v == "" {
			v = fields[1]
		}
		if len(fields) == 2 && fields[0] == "toolchain" {
			v = strings.TrimPrefix(fields[1], "go")
		}
	}
	if v == "" {
		return "", errors.New("go.mod names no Go version")
	}
	if strings.Count(v, ".") == 1 {
		v += ".0"
	}
	return v, nil
}

// nodeLTS is the newest Node.js LTS release, which mise's "lts" installs.
func nodeLTS(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://nodejs.org/dist/index.json", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var rels []struct {
		Version string `json:"version"`
		LTS     any    `json:"lts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return "", err
	}
	for _, r := range rels {
		if b, ok := r.LTS.(bool); !ok || b {
			return r.Version, nil
		}
	}
	return "", errors.New("no LTS release")
}

func readTail(path string) string {
	b, _ := os.ReadFile(path)
	if len(b) > 4000 {
		b = b[len(b)-4000:]
	}
	return string(b)
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return strconv.Itoa(os.Getuid())
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// parseSize reads sizes like 16G, 16GiB or 512M, in bytes.
func parseSize(s string) int64 {
	s = strings.TrimSuffix(strings.TrimSuffix(strings.ToUpper(strings.TrimSpace(s)), "IB"), "B")
	mult := int64(1)
	if s != "" {
		switch s[len(s)-1] {
		case 'K':
			mult = 1 << 10
		case 'M':
			mult = 1 << 20
		case 'G':
			mult = 1 << 30
		case 'T':
			mult = 1 << 40
		}
		if mult > 1 {
			s = s[:len(s)-1]
		}
	}
	n, _ := strconv.ParseFloat(s, 64)
	return int64(n * float64(mult))
}
