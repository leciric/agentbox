package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// rootlessDriver is the spike of agents with no VM and no root: every agent a
// rootless Podman container on the host, from AgentBox's published base
// image, with systemd as its init like an Incus container's. What it stands
// for is a machine runtime of AgentBox's own; Podman is here because it
// already is one (user namespace, a network of its own, a systemd scope with
// cgroup limits), so the numbers come in a day.
//
// An agent's machine is:
//
//   - the base image, copy-on-write (Podman's overlay store);
//   - the host's user mapped onto the image's placeholder user (uid 1000), so
//     what an agent writes in its worktree is the user's, as raw.idmap does
//     in Incus, and every other uid, root included, in the user's subuid
//     range: root in the machine is nobody on the host;
//   - every capability, inside that user namespace only: what an unprivileged
//     Incus container has, and what sudo, apt and Docker need;
//   - its worktree and the repository's .git at their host paths;
//   - volumes for Docker's and containerd's data, since overlayfs can't sit
//     on the machine's own overlay root;
//   - a systemd scope under one slice for every agent, with memory.max on each
//     machine and on the slice: the limits that keep a
//     runaway build from freezing the host, with no root.
type rootlessDriver struct {
	o     *options
	work  string
	r     *runner
	net   string
	image string

	hostMem  int64 // bytes
	agentMax int64 // one machine's memory.max
	totalMax int64 // the slice's, for every machine together
	ctrl     string
	gpuNodes []string
	sliceSet bool

	mu      sync.Mutex
	cgroups map[string]string // agent → its cgroup's memory.current
}

const (
	rootlessLabel = "agentbox-bench=rootless"
	rootlessSlice = "agentbox_bench.slice"
	rootlessUser  = "agent" // the published image's placeholder user, uid 1000
)

func newRootlessDriver(o *options) *rootlessDriver {
	return &rootlessDriver{o: o, work: filepath.Join(o.cache, "rootless"), r: &runner{}, cgroups: map[string]string{}}
}

func (d *rootlessDriver) name() string        { return "rootless" }
func (d *rootlessDriver) vm() vmOps           { return nil }
func (d *rootlessDriver) setRunner(r *runner) { d.r = r }
func (d *rootlessDriver) container(agent string) string {
	return "agentbox-" + agent
}
func (d *rootlessDriver) worktree(agent string) string { return filepath.Join(d.work, "wt", agent) }

// podman runs podman with its temporary files on disk: an import of the base
// image writes 3 GiB of them, and /tmp is often a tmpfs.
func (d *rootlessDriver) podman(ctx context.Context, args ...string) (string, error) {
	return d.r.run(ctx, "", append([]string{"env", "TMPDIR=" + filepath.Join(d.work, "tmp"), "podman"}, args...)...)
}

func (d *rootlessDriver) describe() map[string]any {
	c := map[string]any{"image": d.image, "network": d.net, "repo": d.o.repo,
		"memoryMaxPerAgentGiB": gib(d.agentMax), "memoryMaxAllAgentsGiB": gib(d.totalMax),
		"delegatedControllers": d.ctrl, "gpuNodes": d.gpuNodes}
	if out, err := d.r.run(context.Background(), "", "podman", "--version"); err == nil {
		c["podman"] = strings.TrimSpace(out)
	}
	return c
}

func (d *rootlessDriver) preflight(ctx context.Context) error {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return fmt.Errorf("no published base image for %s", runtime.GOARCH)
	}
	if _, err := exec.LookPath("podman"); err != nil {
		return errors.New("rootless mode needs Podman, the one thing to install (Arch: sudo pacman -S podman; Debian: sudo apt install podman): it stands in for a runtime AgentBox would bring itself")
	}
	if err := os.MkdirAll(filepath.Join(d.work, "tmp"), 0o755); err != nil {
		return err
	}
	out, err := d.podman(ctx, "info", "--format", "{{.Host.Security.Rootless}} {{.Host.CgroupsVersion}} {{.Host.CgroupManager}}")
	if err != nil {
		return fmt.Errorf("podman info: %w", err)
	}
	if f := strings.Fields(out); len(f) != 3 || f[0] != "true" || f[1] != "v2" || f[2] != "systemd" {
		return fmt.Errorf("rootless mode needs rootless Podman on cgroup v2 with the systemd cgroup manager, and podman info says rootless=%s", strings.TrimSpace(out))
	}
	left, err := d.podman(ctx, "ps", "-a", "-q", "--filter", "label="+rootlessLabel)
	if err != nil {
		return err
	}
	if strings.TrimSpace(left) != "" {
		return fmt.Errorf("an earlier run left its machines: run %s --cleanup --modes rootless first", os.Args[0])
	}
	if _, err := os.Stat(filepath.Join(d.work, "repo")); err == nil {
		return fmt.Errorf("%s is already there, left by an earlier run: run %s --cleanup --modes rootless first", d.work, os.Args[0])
	}

	// pasta is Podman's default rootless network; where it doesn't run
	// (it crashes inside an Incus container), slirp4netns does.
	d.net = "pasta"
	if _, err := d.r.run(ctx, "", "pasta", "--version"); err != nil {
		if _, err := exec.LookPath("slirp4netns"); err != nil {
			return errors.New("neither pasta (passt) nor slirp4netns runs here: install passt")
		}
		d.net = "slirp4netns"
	}

	d.hostMem = memTotal()
	if d.hostMem == 0 {
		return errors.New("can't read MemTotal from /proc/meminfo")
	}
	if d.agentMax, err = sizeOf(d.o.rootlessMemory, d.hostMem, max64(d.hostMem/3, 4<<30)); err != nil {
		return fmt.Errorf("--rootless-memory: %w", err)
	}
	if d.totalMax, err = sizeOf(d.o.rootlessTotal, d.hostMem, d.hostMem*6/10); err != nil {
		return fmt.Errorf("--rootless-total: %w", err)
	}
	uid := strconv.Itoa(os.Getuid())
	b, _ := os.ReadFile("/sys/fs/cgroup/user.slice/user-" + uid + ".slice/user@" + uid + ".service/cgroup.controllers")
	d.ctrl = strings.TrimSpace(string(b))
	if !strings.Contains(" "+d.ctrl+" ", " memory ") {
		return fmt.Errorf("systemd doesn't delegate the memory controller to your user (it has %q): the machines would have no memory limit", d.ctrl)
	}
	nodes, _ := filepath.Glob("/dev/dri/*")
	for _, node := range nodes {
		if f, err := os.OpenFile(node, os.O_RDWR, 0); err == nil {
			f.Close()
			d.gpuNodes = append(d.gpuNodes, node)
		}
	}
	return checkFree(d.work, d.o.writeGiB+25)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func memTotal() int64 {
	b, _ := os.ReadFile("/proc/meminfo")
	if m := regexp.MustCompile(`MemTotal:\s+(\d+) kB`).FindSubmatch(b); m != nil {
		kb, _ := strconv.ParseInt(string(m[1]), 10, 64)
		return kb << 10
	}
	return 0
}

// parseSize reads "8G", "512M" or "60%" (of total); "" is def.
func sizeOf(s string, total, def int64) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	mult := int64(1)
	switch last := s[len(s)-1]; last {
	case '%':
		p, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err != nil || p <= 0 || p > 100 {
			return 0, fmt.Errorf("%q isn't a percentage", s)
		}
		return int64(float64(total) * p / 100), nil
	case 'G', 'g':
		mult, s = 1<<30, s[:len(s)-1]
	case 'M', 'm':
		mult, s = 1<<20, s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("%q isn't a size: 8G, 512M or 60%%", s)
	}
	return int64(v * float64(mult)), nil
}

// The published image's manifest (internal/image.Published), as much as the
// download needs.
type publishedParts struct {
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Parts  []struct {
		File   string `json:"file"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"parts"`
}

type publishedManifest struct {
	Version string `json:"version"`
	publishedParts
	Zstd *publishedParts `json:"zstd"`
}

// imageVersion is the base image the repository's AgentBox uses
// (image.Version), unless --rootless-image-version names another.
func (d *rootlessDriver) imageVersion() (string, error) {
	if d.o.rootlessImage != "" {
		return d.o.rootlessImage, nil
	}
	b, err := os.ReadFile(filepath.Join(d.o.repo, "internal", "image", "image.go"))
	if err != nil {
		return "", err
	}
	m := regexp.MustCompile(`(?m)^const Version = "([^"]+)"`).FindSubmatch(b)
	if m == nil {
		return "", errors.New("no image.Version in internal/image/image.go: pass --rootless-image-version")
	}
	return string(m[1]), nil
}

func (d *rootlessDriver) setup(ctx context.Context, m *modeRun) error {
	version, err := d.imageVersion()
	if err != nil {
		return err
	}
	d.image = "localhost/agentbox-bench-base:" + version
	dl := filepath.Join(d.o.cache, "dl", "image-"+version)
	tarball := ""
	note := ""
	secs, err := m.setupStep(ctx, "download AgentBox's published base image", func(ctx context.Context) error {
		var cached bool
		tarball, cached, err = d.download(ctx, version, dl)
		if cached {
			note = "the base image was already downloaded (kept in " + dl + "): 0 s here, as long as the download takes on a first install"
		}
		return err
	})
	if err != nil {
		return err
	}
	_ = secs
	if note != "" {
		m.note("setup.total", note)
	}
	if _, err := m.setupStep(ctx, "import it into Podman", func(ctx context.Context) error { return d.importImage(ctx, tarball) }); err != nil {
		return err
	}
	return m.step0(ctx, "clone the repository the agents build", func(ctx context.Context) error {
		_, err := d.r.run(ctx, "", "git", "clone", "-q", d.o.repo, filepath.Join(d.work, "repo"))
		return err
	})
}

// download fetches the image's parts from the repository's GitHub release,
// checks each against the manifest and joins them. The zstd copy when zstd is
// installed: a third smaller.
func (d *rootlessDriver) download(ctx context.Context, version, dir string) (string, bool, error) {
	arch := runtime.GOARCH
	base := "https://github.com/leciric/agentbox/releases/download/image-" + version + "/"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, err
	}
	var man publishedManifest
	mf := filepath.Join(dir, "image-"+arch+".json")
	if _, err := os.Stat(mf); err != nil {
		if err := fetchSum(ctx, base+"image-"+arch+".json", mf, ""); err != nil {
			return "", false, fmt.Errorf("no published image %s for %s (pass --rootless-image-version): %w", version, arch, err)
		}
	}
	b, err := os.ReadFile(mf)
	if err != nil {
		return "", false, err
	}
	if err := json.Unmarshal(b, &man); err != nil {
		return "", false, err
	}
	p := man.publishedParts
	if _, err := exec.LookPath("zstd"); err == nil && man.Zstd != nil {
		p = *man.Zstd
	}
	out := filepath.Join(dir, p.File)
	if st, err := os.Stat(out); err == nil && st.Size() == p.Size {
		return out, true, nil
	}
	f, err := os.Create(out + ".part")
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	for _, part := range p.Parts {
		d.r.logf("downloading %s", part.File)
		pf := filepath.Join(dir, part.File)
		if err := fetchSum(ctx, base+part.File, pf, part.SHA256); err != nil {
			return "", false, err
		}
		in, err := os.Open(pf)
		if err != nil {
			return "", false, err
		}
		_, err = io.Copy(f, in)
		in.Close()
		os.Remove(pf)
		if err != nil {
			return "", false, err
		}
	}
	if err := f.Close(); err != nil {
		return "", false, err
	}
	return out, false, os.Rename(out+".part", out)
}

func fetchSum(ctx context.Context, url, to, sum string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	f, err := os.Create(to)
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); sum != "" && got != sum {
		os.Remove(to)
		return fmt.Errorf("%s: sha256 is %s, not the manifest's %s", url, got, sum)
	}
	return nil
}

// importImage makes a Podman image of the published one, which is as `incus
// image export` writes it: the machine's files under rootfs/. They're
// unpacked and packed again inside Podman's user namespace, so owners and
// file capabilities survive.
func (d *rootlessDriver) importImage(ctx context.Context, tarball string) error {
	x := filepath.Join(d.work, "tmp", "rootfs")
	unpack := "zstd -dc"
	if strings.HasSuffix(tarball, ".gz") {
		unpack = "gzip -dc"
	}
	script := fmt.Sprintf(`set -euo pipefail
rm -rf %[1]s && mkdir -p %[1]s
%[2]s %[3]s | tar -x -C %[1]s --numeric-owner --xattrs --xattrs-include='*' rootfs
tar -C %[1]s/rootfs --numeric-owner --xattrs --xattrs-include='*' -c . | podman import -q - %[4]s
rm -rf %[1]s`, shq(x), unpack, shq(tarball), shq(d.image))
	_, err := d.podman(ctx, "unshare", "bash", "-c", script)
	return err
}

func (d *rootlessDriver) create(ctx context.Context, agent string) error {
	wt, repo := d.worktree(agent), filepath.Join(d.work, "repo")
	if _, err := d.r.run(ctx, "", "git", "-C", repo, "worktree", "add", "-q", "-b", "agentbox-bench/"+agent, wt); err != nil {
		return err
	}
	name := d.container(agent)
	// memory.max and no memory.high: over memory.high with nothing left to
	// reclaim, a process isn't killed but throttled almost to a stop (a
	// first run of the memory check sat there for 38 minutes), which looks
	// like a hung agent. Over memory.max the kernel kills it at once.
	args := []string{"run", "-d", "--name", name, "--hostname", agent, "--label", rootlessLabel,
		"--systemd=always", "--userns=keep-id:uid=1000,gid=1000", "--user", "0:0",
		"--cap-add", "all", "--security-opt", "label=disable", "--security-opt", "unmask=all",
		"--network", d.net, "--cgroup-parent", rootlessSlice,
		// Swap equal to memory means none of its own: what the cap says is
		// what the machine gets.
		"--memory", strconv.FormatInt(d.agentMax, 10), "--memory-swap", strconv.FormatInt(d.agentMax, 10),
		"--pids-limit", "16384", "--tmpfs", "/t:size=2g,mode=1777",
		"-v", wt + ":" + wt, "-v", filepath.Join(repo, ".git") + ":" + filepath.Join(repo, ".git"),
		// Podman has the machine's network up before systemd starts;
		// networkd would wait two minutes for an address of its own.
		"-v", "/dev/null:/etc/systemd/system/systemd-networkd-wait-online.service",
		"-v", name + "-docker:/var/lib/docker", "-v", name + "-containerd:/var/lib/containerd"}
	if strings.Contains(" "+d.ctrl+" ", " io ") {
		args = append(args, "--blkio-weight", "50")
	}
	// The GPU, as host mode's Incus gpu device gave it: the nodes
	// themselves, no Venus. Rootless Podman binds each over; only those
	// the user can open are any use.
	for _, node := range d.gpuNodes {
		args = append(args, "--device", node)
	}
	args = append(args, d.image, "/sbin/init")
	if _, err := d.podman(ctx, args...); err != nil {
		return err
	}
	if path, err := d.podman(ctx, "inspect", name, "--format", "{{.State.CgroupPath}}"); err == nil {
		d.mu.Lock()
		d.cgroups[agent] = filepath.Join("/sys/fs/cgroup", strings.TrimSpace(path), "memory.current")
		d.mu.Unlock()
	}
	// The slice every machine is under exists from the first one on: its
	// limits are the host's protection whatever each machine's own are.
	if !d.sliceSet {
		if _, err := d.r.run(ctx, "", "systemctl", "--user", "set-property", "--runtime", rootlessSlice,
			"MemoryMax="+strconv.FormatInt(d.totalMax, 10), "MemorySwapMax=0"); err != nil {
			d.r.logf("the slice's memory limit wasn't set: %v", err)
		} else {
			d.sliceSet = true
		}
	}
	if err := d.ready(ctx, agent); err != nil {
		return err
	}
	// sudo's account check runs unix_chkpwd, which a host with AppArmor's
	// unix-chkpwd profile (Debian 13, Ubuntu 25.04) confines by its path,
	// inside containers too, where it then can't map libc. An image built
	// for this runtime would carry this line.
	_, err := d.podman(ctx, "exec", name, "sed", "-i", "1a account sufficient pam_permit.so", "/etc/pam.d/sudo")
	return err
}

// ready waits until the machine's systemd has started its services, which is
// when an agent's tool could be started in it.
func (d *rootlessDriver) ready(ctx context.Context, agent string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		out, _ := d.podmanQuiet(ctx, "exec", d.container(agent), "systemctl", "is-active", "multi-user.target")
		if strings.TrimSpace(out) == "active" {
			return nil
		}
		if sleepCtx(ctx, 50*time.Millisecond) != nil {
			return fmt.Errorf("%s's systemd didn't reach multi-user.target", agent)
		}
	}
}

// podmanQuiet is podman without the log: for polling.
func (d *rootlessDriver) podmanQuiet(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "podman", args...).Output()
	return string(out), err
}

func (d *rootlessDriver) action(ctx context.Context, agent, action string) error {
	name := d.container(agent)
	switch action {
	case "start":
		if _, err := d.podman(ctx, "start", name); err != nil {
			return err
		}
		return d.ready(ctx, agent)
	case "stop":
		// systemd's own signal for a clean shutdown, as Incus sends.
		_, err := d.podman(ctx, "stop", "-t", "30", name)
		return err
	case "pause":
		_, err := d.podman(ctx, "pause", name)
		return err
	case "resume":
		_, err := d.podman(ctx, "unpause", name)
		return err
	}
	return fmt.Errorf("unknown action %q", action)
}

func (d *rootlessDriver) exec(ctx context.Context, agent, script string) error {
	_, err := d.podman(ctx, "exec", "-u", rootlessUser, "-w", d.worktree(agent), d.container(agent), "bash", "-lc", script)
	return err
}

func (d *rootlessDriver) held() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	var total int64
	for _, path := range d.cgroups {
		b, err := os.ReadFile(path)
		if err != nil {
			continue // stopped: its scope is gone
		}
		v, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		total += v
	}
	return total
}

// hostChecks are what only the host can say about this mode.
func (d *rootlessDriver) hostChecks() []check {
	io := strings.Contains(" "+d.ctrl+" ", " io ")
	c := check{Name: "IO limits (io.weight) without root", OK: io, Detail: "systemd delegates \"" + d.ctrl + "\" to your user"}
	if !io {
		c.Detail += `: IO needs a one-time root drop-in, /etc/systemd/system/user@.service.d/delegate.conf with "[Service]\nDelegate=cpu cpuset io memory pids"`
	}
	return []check{c, {Name: "No root, no setup step", OK: true, Detail: "everything ran as your user; Podman is the one package, in place of a runtime AgentBox would ship"}}
}

func (d *rootlessDriver) cleanup(ctx context.Context) error {
	if _, err := exec.LookPath("podman"); err != nil {
		return nil
	}
	_ = os.MkdirAll(filepath.Join(d.work, "tmp"), 0o755)
	var errs []error
	sh := func(line string) {
		if _, err := d.r.sh(ctx, line); err != nil {
			errs = append(errs, err)
		}
	}
	sh("ids=$(podman ps -a -q --filter label=" + rootlessLabel + "); [ -z \"$ids\" ] || podman rm -f -t 5 $ids")
	sh("vols=$(podman volume ls -q | grep -E '^agentbox-bench-[0-9]+-(docker|containerd)$' || true); [ -z \"$vols\" ] || podman volume rm -f $vols")
	if !d.o.keepImage {
		sh("imgs=$(podman images -q localhost/agentbox-bench-base); [ -z \"$imgs\" ] || podman rmi -f $imgs")
	}
	// Go's module cache is read-only, and files root made in a machine are
	// a subuid's: only inside the user namespace can they be removed.
	sh("[ ! -d " + shq(d.work) + " ] || podman unshare rm -rf " + shq(d.work))
	return errors.Join(errs...)
}
