// Package hostvm runs AgentBox in a Linux VM, with this machine as its front
// end. Nothing of AgentBox is ported: the daemon, Incus and every agent run in
// the VM exactly as they run on a Linux machine, and the agentbox command here
// is a front end for that VM. The VM has one of three drivers:
//
//   - Lima, on a Mac (D92), and on Linux with AGENTBOX_FRONT_END=vm, which is
//     how the Mac's front end is tested without a Mac.
//   - Cloud Hypervisor (package chv), on every Linux machine: `agentbox vm
//     init` makes it, and every other command runs in it. Installing Incus on
//     the machine itself (`agentbox host setup`) is what the VM does inside;
//     a machine that did it on the host from before is the one exception
//     (Front), until `agentbox vm migrate` moves it into the VM. chvdriver.go
//     is its side of this package.
//   - vz, on a Mac whose user asked for it (`agentbox vm init --driver vz`):
//     experimental. Apple's Virtualization framework, driven by AgentBox
//     itself under package chv's supervisor instead of by Lima (chv/vz.go),
//     with a chv.Config like Cloud Hypervisor's, which is what makes the Mac
//     use it rather than Lima (useLima).
//
// What they share is here: the binary kept in the VM, the host setup done in
// it, what its agentbox is told about the host, and forwarding commands. What
// differs is how a command gets into the VM (exec, execLog, command) and how
// the VM is started and stopped.
//
//   - `agentbox vm …` makes, starts, stops and removes the VM (cmd.go).
//   - Every other command runs in the VM, in the same working directory, with
//     the terminal, the exit status and Ctrl-C of a local one (Forward).
//   - The daemon's unix socket is forwarded by Lima to where the Mac's app and
//     command look for it, so the app talks to it as to a local daemon (D19).
//   - The Mac's home directory is shared into the VM at the same path, so a
//     project keeps its path, which is what AgentBox mounts into each agent.
//     The daemon's state stays on the VM's own disk; agents' worktrees go on
//     the share, where the Mac's editors can open them (paths.Worktrees), and
//     so does their media, which the app opens in the host's default app or
//     shows in its folder (paths.Media).
//
// The VM's agentbox is the Linux build this front end ships beside it, kept
// identical: a command first compares the two and installs the Mac's copy
// when they differ, so updating the app updates the VM.
package hostvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"text/template"
	"time"

	"golang.org/x/term"

	"agentbox/internal/android"
	"agentbox/internal/api"
	"agentbox/internal/hostos"
	"agentbox/internal/hostvm/chv"
	"agentbox/internal/paths"
	"agentbox/internal/report"
)

const (
	// DefaultName is the Lima instance AgentBox's VM is; AGENTBOX_VM changes it.
	DefaultName = "agentbox"
	// LinuxBinary is the Linux build of agentbox the front end installs in
	// the VM, kept next to the front end itself.
	LinuxBinary = "agentbox-linux"
	// vmBinary is where it goes in the VM.
	vmBinary = "/usr/local/bin/agentbox"
)

// ErrNotCreated is a VM that `agentbox vm init` hasn't made yet.
var ErrNotCreated = errors.New("AgentBox's Linux VM isn't set up: run agentbox vm init")

//go:embed agentbox.yaml
var definition string

// Front reports whether this process is the front end of a VM rather than
// AgentBox itself: always on macOS; on Linux when AGENTBOX_FRONT_END=vm, which
// is how the Lima VM is tested without a Mac; and on a Linux machine of its
// own, which runs AgentBox in Cloud Hypervisor's VM: the VM `agentbox vm
// init` made, or the one it is yet to make. It runs on every command, so on
// Linux it is a stat or two.
//
// What runs AgentBox itself on Linux, and so isn't a front end:
//   - The agentbox in a VM, whatever its files say: its front end told it so
//     (hostos.Env), or the VM itself does (hostos.OS). Its HOME can be the
//     host's home, shared at the same path, with the host's VM in it: a front
//     end run there would take that VM for its own, and start it again under
//     the running one.
//   - A host-mode installation from before the VM was the only way: a machine
//     with a state.db of its own and no VM. Its agents keep working there
//     until `agentbox vm migrate` moves it into the VM (HostInstall).
//   - root: `sudo agentbox host setup` and the daemon's budget unit are
//     host setup's, in the VM and on a host-mode installation alike.
//   - An agent's machine, where agentbox talks to its daemon through
//     api.InAgentSocket.
//   - AGENTBOX_FRONT_END=host, for CI and anyone else who runs AgentBox's
//     internals on a machine of their own on purpose.
func Front() bool {
	if runtime.GOOS == "darwin" || os.Getenv("AGENTBOX_FRONT_END") == "vm" {
		return true
	}
	if runtime.GOOS != "linux" || os.Getenv("AGENTBOX_FRONT_END") != "" || hostos.InVM() || os.Geteuid() == 0 || inAgent() {
		return false
	}
	p, err := paths.Default()
	if err != nil {
		return false
	}
	return chv.Exists(p, env("AGENTBOX_VM", DefaultName)) || !HostInstall(p)
}

// HostInstall reports whether this Linux machine runs AgentBox itself, from
// before AgentBox ran in a VM on Linux: whether it has a state.db of its own,
// which is also what `agentbox vm migrate` moves (readHostInstall).
func HostInstall(p paths.Paths) bool {
	_, err := os.Stat(p.StateDB())
	return err == nil
}

// inAgentMarker is api.InAgentMarker; a variable for tests, which may run in
// an agent.
var inAgentMarker = api.InAgentMarker

// inAgent reports whether this is an agent's machine: provision.sh's marker,
// not api.InAgentSocket, which a boot race can hide for a while after the
// machine starts (replugHiddenSocket), wrongly making this look like a front
// end with no VM of its own.
func inAgent() bool {
	_, err := os.Stat(inAgentMarker)
	return err == nil
}

// Handles reports whether a command line (os.Args[1:]) is the front end's to
// run: every command on a front end, and on a host-mode installation (Front)
// `agentbox vm …` too, since `vm migrate` is what moves it into the VM and
// `vm status --json` is how the app asks which it is. In AgentBox's VM, it is
// only so Main can say that `agentbox vm` isn't for in there.
func Handles(args []string) bool {
	if Front() {
		return true
	}
	return runtime.GOOS == "linux" && len(args) > 0 && args[0] == "vm" &&
		os.Getenv("AGENTBOX_FRONT_END") == "" && !hostos.WSL()
}

// Help reports whether a command line only asks for help with agentbox's own
// commands (not agentbox vm's, which are the front end's): none at all, help,
// or -h or --help before any "--". The front end answers those itself rather
// than forward them, which would start the VM.
func Help(args []string) bool {
	if len(args) == 0 || args[0] == "help" {
		return true
	}
	if args[0] == "vm" {
		return false
	}
	for _, a := range args {
		switch a {
		case "--":
			return false
		case "-h", "--help":
			return true
		}
	}
	return false
}

// errInVM is `agentbox vm …` in AgentBox's own VM.
var errInVM = errors.New("this is AgentBox's VM: agentbox vm manages it from the host, and doesn't run in it")

// inVM reports whether this is AgentBox's VM (not WSL, which is hostwsl's),
// where no front end runs.
func inVM() bool {
	return runtime.GOOS == "linux" && os.Getenv("AGENTBOX_FRONT_END") == "" && hostos.InVM() && !hostos.WSL()
}

// useLima reports whether this machine's VM is Lima's rather than one of
// package chv's: Cloud Hypervisor's on Linux, or on a Mac whose VM `agentbox
// vm init --driver vz` made, the vz driver's.
func useLima() bool {
	if os.Getenv("AGENTBOX_FRONT_END") == "vm" {
		return true
	}
	return runtime.GOOS == "darwin" && !vzMade()
}

// vzMade reports whether this Mac's VM is the vz driver's: whether `agentbox
// vm init --driver vz` saved its Config. It is a stat, on every command.
func vzMade() bool {
	p, err := paths.Default()
	return err == nil && chv.Exists(p, env("AGENTBOX_VM", DefaultName))
}

// VM is AgentBox's VM, seen from the host.
type VM struct {
	Limactl string // Lima's: the limactl binary
	Name    string // the Lima instance, or the Cloud Hypervisor VM's name
	// Home is the host user's home directory, shared into the VM at the same
	// path.
	Home string
	// Paths are the host's AgentBox paths: the socket Lima forwards the
	// daemon's to, and the worktrees directory on the share.
	Paths paths.Paths
	// Binary is the Linux agentbox kept in the VM.
	Binary string
	// VMType is Lima's vmType for a VM `vm init` makes: AGENTBOX_VM_TYPE, or
	// "" for createVM to choose (krunkit.go), which leaves it "" for Lima's
	// default (vz on a Mac) when it doesn't choose krunkit. The Linux tests set
	// qemu.
	VMType string
	// Log is where progress goes: the front end's stderr.
	Log io.Writer
	// CHV is set when the VM is Cloud Hypervisor's, on a Linux host; nil when
	// it is Lima's.
	CHV *CHV
}

// Driver is api.VMDriverLima, api.VMDriverCloudHypervisor or api.VMDriverVZ.
func (v *VM) Driver() string {
	if v.CHV != nil {
		return v.CHV.Config.DriverName()
	}
	return api.VMDriverLima
}

// New describes this machine's VM: Lima's, finding limactl and the Linux
// binary, or Cloud Hypervisor's, from the Config `agentbox vm init` saved
// (ErrNotCreated, with the VM's name and paths set, before there is one).
func New() (*VM, error) {
	if !useLima() {
		return newCHV()
	}
	p, err := paths.Default()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	// The share's path as the VM sees it, with no symlinks in it.
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	vm := &VM{
		Name:   env("AGENTBOX_VM", DefaultName),
		Home:   home,
		Paths:  p,
		VMType: os.Getenv("AGENTBOX_VM_TYPE"),
		Log:    os.Stderr,
	}
	vm.Limactl, err = FindLimactl()
	if err != nil {
		return vm, err
	}
	if runtime.GOOS == "darwin" {
		krunkitOnPath()
	}
	vm.Binary, err = FindLinuxBinary()
	return vm, err
}

// FindLimactl looks for Lima where Homebrew puts it, as well as on PATH: an
// app opened from the Finder doesn't get the shell's PATH.
func FindLimactl() (string, error) {
	if p := os.Getenv("AGENTBOX_LIMACTL"); p != "" {
		return p, nil
	}
	if p, err := exec.LookPath("limactl"); err == nil {
		return p, nil
	}
	for _, p := range []string{"/opt/homebrew/bin/limactl", "/usr/local/bin/limactl"} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("AgentBox runs in a Linux VM made with Lima, which isn't installed: brew install lima")
}

// FindLinuxBinary finds the Linux agentbox to put in the VM: next to this
// binary (the app and the release keep them together), or AGENTBOX_LINUX_BINARY.
func FindLinuxBinary() (string, error) {
	if p := os.Getenv("AGENTBOX_LINUX_BINARY"); p != "" {
		return p, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	p := filepath.Join(filepath.Dir(exe), LinuxBinary)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("no Linux agentbox next to %s to run in the VM: the app ships one; from a checkout, GOOS=linux GOARCH=%s go build -o %s ./cmd/agentbox", exe, runtime.GOARCH, p)
	}
	return p, nil
}

// State is what `limactl list` says of the instance.
type State struct {
	Exists bool   `json:"exists"`
	Status string `json:"status,omitempty"` // Running, Stopped, Broken
	Dir    string `json:"dir,omitempty"`
	CPUs   int    `json:"cpus,omitempty"`
	Memory int64  `json:"memory,omitempty"` // bytes
	Disk   int64  `json:"disk,omitempty"`   // bytes
	Arch   string `json:"arch,omitempty"`
	VMType string `json:"vmType,omitempty"` // vz, krunkit, qemu
}

func (v *VM) State(ctx context.Context) (State, error) {
	out, err := v.lima(ctx, nil, "list", "--format", "json")
	if err != nil {
		return State{}, err
	}
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var inst struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Dir    string `json:"dir"`
			CPUs   int    `json:"cpus"`
			Memory int64  `json:"memory"`
			Disk   int64  `json:"disk"`
			Arch   string `json:"arch"`
			VMType string `json:"vmType"`
		}
		if err := dec.Decode(&inst); errors.Is(err, io.EOF) {
			return State{}, nil
		} else if err != nil {
			return State{}, fmt.Errorf("reading limactl list: %w", err)
		}
		if inst.Name == v.Name {
			return State{Exists: true, Status: inst.Status, Dir: inst.Dir, CPUs: inst.CPUs, Memory: inst.Memory, Disk: inst.Disk, Arch: inst.Arch, VMType: inst.VMType}, nil
		}
	}
}

// Size is what the VM is made with.
type Size struct {
	CPUs   int
	Memory string // like 8GiB
	Disk   string // like 100GiB
}

// DefaultSize is half the host's cores (two to eight), 8 GiB and a 100 GiB
// disk, which Lima allocates as it's used.
func DefaultSize() Size {
	return Size{CPUs: defaultCPUs(numCPU()), Memory: "8GiB", Disk: "100GiB"}
}

// defaultCPUs is what a VM is given of a host's cores: half, two to eight.
func defaultCPUs(host int) int { return min(max(host/2, 2), 8) }

// numCPU is the host's cores.
var numCPU = runtime.NumCPU

// Definition is the Lima YAML the VM is made from.
func (v *VM) Definition(size Size) (string, error) {
	t, err := template.New("agentbox.yaml").Delims("[[", "]]").Parse(definition)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, struct {
		Name, Home, Socket, VMType, Memory, Disk string
		CPUs                                     int
	}{v.Name, v.Home, v.Paths.Socket(), v.VMType, size.Memory, size.Disk, size.CPUs})
	return b.String(), err
}

// Create makes the VM from its definition, and starts it.
func (v *VM) Create(ctx context.Context, size Size) error {
	def, err := v.Definition(size)
	if err != nil {
		return err
	}
	file := filepath.Join(v.Paths.Config, "vm", v.Name+".yaml")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(file, []byte(def), 0o644); err != nil {
		return err
	}
	// Lima makes the host's end of the socket; its directory has to exist.
	if err := os.MkdirAll(filepath.Dir(v.Paths.Socket()), 0o700); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(v.Log, "==> Making the VM %s (%d CPUs, %s, %s disk) from %s\n", v.Name, size.CPUs, size.Memory, size.Disk, file)
	return v.limaLog(ctx, "create", "--tty=false", "--name", v.Name, file)
}

func (v *VM) Start(ctx context.Context) error {
	if v.CHV != nil {
		return v.CHV.start(ctx, v)
	}
	if err := os.MkdirAll(filepath.Dir(v.Paths.Socket()), 0o700); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(v.Log, "==> Starting AgentBox's VM (%s)\n", v.Name)
	return v.limaLog(ctx, "start", "--tty=false", v.Name)
}

// Stop stops the VM, and with it the daemon and every agent. With agents set,
// a Cloud Hypervisor VM's supervisor stops every running agent through the
// daemon first, so none restarts with the VM. A Lima VM has no such step:
// agents is ignored there, and Incus restarts in it the agents that ran.
func (v *VM) Stop(ctx context.Context, agents bool) error {
	if v.CHV != nil {
		return chvStop(ctx, v.CHV.Config, v.CHV.Layout, v.Paths, agents, v.Log)
	}
	return v.limaLog(ctx, "stop", v.Name)
}

func (v *VM) Delete(ctx context.Context) error {
	if v.CHV != nil {
		return v.CHV.delete(ctx, v)
	}
	return v.limaLog(ctx, "delete", "--force", v.Name)
}

// Up starts the VM unless it runs. A VM that doesn't exist is ErrNotCreated.
func (v *VM) Up(ctx context.Context) error {
	unlock, err := v.lock(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	return v.up(ctx)
}

func (v *VM) up(ctx context.Context) error {
	if v.CHV != nil {
		return v.CHV.up(ctx, v)
	}
	st, err := v.State(ctx)
	if err != nil {
		return err
	}
	switch {
	case !st.Exists:
		return ErrNotCreated
	case st.Status == "Running":
		return nil
	case st.Status == "Broken":
		return fmt.Errorf("AgentBox's VM is broken, Lima says: see limactl list, and %s", filepath.Join(st.Dir, "ha.stderr.log"))
	}
	return v.Start(ctx)
}

// lock takes the VM's lock, shared by everything that would start the VM and
// held alone by a resize, which stops it and has to keep it stopped until Lima
// has changed it: the app, finding no daemon mid-resize, runs agentbox daemon
// start, and that waits here instead of starting the VM under the resize. A
// command that has to wait says so. unlock lets it go.
func (v *VM) lock(ctx context.Context, exclusive bool) (unlock func(), err error) {
	file := filepath.Join(v.Paths.Config, "vm", v.Name+".lock")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	for waited := false; ; waited = true {
		err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("locking AgentBox's VM: %w", err)
		}
		if !waited {
			if exclusive {
				_, _ = fmt.Fprintln(v.Log, "==> Waiting for the agentbox commands using the VM to finish with it")
			} else {
				_, _ = fmt.Fprintln(v.Log, "==> Waiting for AgentBox's VM: it's being resized or set up")
			}
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Digest is the SHA256 of the Linux binary the front end ships.
func (v *VM) Digest() (string, error) {
	f, err := os.Open(v.Binary)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Current reports whether the VM's agentbox is the one the front end ships.
func (v *VM) Current(ctx context.Context) (bool, error) {
	want, err := v.Digest()
	if err != nil {
		return false, err
	}
	have, _ := v.exec(ctx, nil, "sh", "-c", "sha256sum "+vmBinary+" 2>/dev/null | cut -d' ' -f1")
	return strings.TrimSpace(have) == want, nil
}

// Install puts the Linux binary in the VM, replacing the old one in one
// rename: a daemon running from it keeps running the old file until it's
// restarted (the app restarts a daemon of another version itself).
func (v *VM) Install(ctx context.Context) error {
	f, err := os.Open(v.Binary)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(v.Log, "==> Installing %s in the VM as %s\n", v.Binary, vmBinary)
	script := fmt.Sprintf(`set -e; t=%[1]s.new.$$; cat >"$t"; chmod 0755 "$t"; mv -f "$t" %[1]s`, vmBinary)
	_, err = v.exec(ctx, f, "sudo", "sh", "-c", script)
	return err
}

// Ready starts the VM if it's stopped, and brings its agentbox up to date:
// what every forwarded command does first.
func (v *VM) Ready(ctx context.Context) error {
	unlock, err := v.lock(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	return v.ready(ctx)
}

func (v *VM) ready(ctx context.Context) error {
	if err := v.up(ctx); err != nil {
		return err
	}
	current, err := v.Current(ctx)
	if err != nil {
		return err
	}
	if !current {
		return v.Install(ctx)
	}
	return nil
}

// bridgeSubnet is the Incus bridge's address and subnet in the Lima VM.
const bridgeSubnet = "10.87.0.1/24"

// guestUser is the VM's user, whom host setup maps agents' files to.
func (v *VM) guestUser(ctx context.Context) (string, error) {
	if v.CHV != nil {
		return v.CHV.Config.User, nil
	}
	// Lima names the VM's user after the host's, unless that name won't do
	// on Linux: ask the VM rather than assume.
	guest, err := v.exec(ctx, nil, "id", "-un")
	return strings.TrimSpace(guest), err
}

// bridge is the subnet host setup gives Incus's bridge in the VM.
func (v *VM) bridge() string {
	if v.CHV != nil {
		return chv.BridgeSubnet
	}
	return bridgeSubnet
}

// took says how long a step of setting the VM up took, for whoever is waiting
// on it (and for comparing the two drivers).
func (v *VM) took(start time.Time) {
	_, _ = fmt.Fprintf(v.Log, "    (took %s)\n", time.Since(start).Round(100*time.Millisecond))
}

// Setup is everything `agentbox vm init` does after the VM runs: AgentBox's
// binary, the same host setup a Linux machine gets (Incus, its btrfs pool, the
// bridge, the user mapping), the host's git identity, the host's settings for
// the VM's login shells, and the daemon.
func (v *VM) Setup(ctx context.Context) error { return v.setup(ctx, true) }

// setup is Setup, starting the daemon only with daemon set.
func (v *VM) setup(ctx context.Context, daemon bool) error {
	start := time.Now()
	if err := v.Install(ctx); err != nil {
		return err
	}
	v.took(start)
	guest, err := v.guestUser(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(v.Log, "==> Host setup in the VM: Incus, its storage and network, and the user mapping")
	start = time.Now()
	// Incus can't pick the bridge's subnet here: it rules out any subnet where
	// an address answers a ping, and Lima's user-mode network answers them all.
	// The VM's only network is Lima's 192.168.5.0/24 (passt's 10.0.2.0/24 for
	// Cloud Hypervisor), so a fixed one is safe.
	if err := v.execLog(ctx, "sudo", vmBinary, "host", "setup", "--user", guest, "--bridge-subnet", v.bridge()); err != nil {
		return fmt.Errorf("host setup in the VM: %w", err)
	}
	v.took(start)
	// The VM's user has a home of its own; the daemon reads git's identity
	// from there, for the commits it makes (snapshots, the agents' identity).
	if _, err := os.Stat(filepath.Join(v.Home, ".gitconfig")); err == nil {
		link := fmt.Sprintf(`[ -e ~/.gitconfig ] && [ ! -L ~/.gitconfig ] || ln -sfn %s ~/.gitconfig`, shellQuote(filepath.Join(v.Home, ".gitconfig")))
		if _, err := v.exec(ctx, nil, "sh", "-c", link); err != nil {
			return fmt.Errorf("linking your git identity into the VM: %w", err)
		}
	}
	// A daemon started by hand in the VM, without the front end's settings,
	// would put new worktrees on the VM's disk instead of the share.
	if err := v.writeProfile(ctx); err != nil {
		return err
	}
	if !daemon {
		return nil
	}
	_, _ = fmt.Fprintln(v.Log, "==> Starting the daemon")
	start = time.Now()
	if err := v.execLog(ctx, append([]string{"env"}, append(v.forwardEnv(), vmBinary, "daemon", "start")...)...); err != nil {
		return err
	}
	v.took(start)
	return nil
}

// hostOnly are the AGENTBOX_ settings that describe the host's side: the
// front end's own, and paths that only mean something on the host.
var hostOnly = map[string]bool{
	"AGENTBOX_FRONT_END": true, "AGENTBOX_VM": true, "AGENTBOX_VM_TYPE": true,
	"AGENTBOX_LIMACTL": true, "AGENTBOX_LINUX_BINARY": true, "AGENTBOX_BIN": true,
	"AGENTBOX_SOCKET": true, "AGENTBOX_WORKTREES": true, "AGENTBOX_MEDIA": true, hostos.Env: true, hostos.HomeEnv: true, hostos.VMDisksEnv: true,
	vmMemoryCapEnv: true,
}

// vmMemoryCapEnv is agent.VMMemoryCapEnv, which package agent reads in the
// VM; hostvm doesn't import package agent for one name.
const vmMemoryCapEnv = "AGENTBOX_VM_MEMORY_CAP"

// vmEnv is what the VM's agentbox has to be told about the host whoever runs
// it: the host's OS, its home directory, and where the worktrees and media go. A Cloud
// Hypervisor VM's is also told the most memory it may be given, which is what
// its agents' default limits are shares of (agent.HostMemory): its own
// /proc/meminfo only has what it was granted so far. And where the host's
// Android SDK is, which its agents run emulators from (androidSDKEnv). A vz
// VM's is on a Mac, like Lima's, and has no Android either: it's only told
// its cap, which its balloon keeps it under.
func (v *VM) vmEnv() []string {
	env := []string{
		hostos.Env + "=" + runtime.GOOS,
		hostos.HomeEnv + "=" + v.Home,
		"AGENTBOX_WORKTREES=" + v.Paths.Worktrees(),
		"AGENTBOX_MEDIA=" + v.Paths.Media(),
	}
	if v.CHV != nil {
		env = append(env, fmt.Sprintf("%s=%d", vmMemoryCapEnv, v.CHV.Config.MemoryCap))
		env = append(env, report.VMLogEnv+"="+v.CHV.Layout.Log())
		env = append(env, hostos.VMDisksEnv+"="+v.CHV.Layout.Dir())
	}
	if v.CHV != nil && !v.CHV.Config.VZ() {
		env[0] = hostos.Env + "=" + hostos.Linux
		env = append(env, v.androidSDKEnv()...)
	}
	return env
}

// androidSDKEnvName is what the VM's daemon looks for the Android SDK in first
// (android.Candidates).
const androidSDKEnvName = "AGENTBOX_ANDROID_SDK"

// androidSDKEnv tells a Cloud Hypervisor VM's agentbox where the host's
// Android SDK is: found here, with the host's settings (ANDROID_HOME and the
// rest, which the VM doesn't have) and the host's home, the way a host-mode
// daemon finds it. The VM sees it at the same path when it's in the shared
// home; when it isn't, the VM's daemon says that it can't see it
// (android.FindSharedSDK). Symlinks are resolved here, since the VM can't
// follow one out of the home. Nothing when the host has no SDK: the VM's
// daemon still looks in the home's Android/Sdk, where one installed later
// usually goes.
func (v *VM) androidSDKEnv() []string {
	sdk, err := android.FindSDK(android.Candidates(os.Getenv, v.Home))
	if err != nil {
		return nil
	}
	path := sdk.Path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return []string{androidSDKEnvName + "=" + path}
}

// profile sets vmEnv in the VM's login shells, so an agentbox run by hand in
// `agentbox vm shell` (or a daemon it starts) agrees with the front end's.
func (v *VM) profile() string {
	var b strings.Builder
	b.WriteString("# Written by agentbox vm init: what the VM's agentbox is told about the host.\n")
	for _, kv := range v.vmEnv() {
		name, value, _ := strings.Cut(kv, "=")
		fmt.Fprintf(&b, "export %s=%s\n", name, shellQuote(value))
	}
	return b.String()
}

// writeProfile puts profile in the VM, for its login shells: by Setup, and again
// when vmEnv may have changed (a Cloud Hypervisor VM's memory cap).
func (v *VM) writeProfile(ctx context.Context) error {
	if _, err := v.exec(ctx, strings.NewReader(v.profile()), "sudo", "sh", "-c", `cat >/etc/profile.d/agentbox-host.sh`); err != nil {
		return fmt.Errorf("writing the host's settings into the VM: %w", err)
	}
	return nil
}

// forwardEnv is what every command in the VM is told about the host: vmEnv,
// and every other AGENTBOX_ setting of the host's (AGENTBOX_ENV,
// AGENTBOX_PREVIEW_ADDR, AGENTBOX_IMAGE_URL…), which mean the same in the VM.
// What vmEnv sets wins over the host's own setting of the same name.
func (v *VM) forwardEnv() []string {
	env := v.vmEnv()
	set := map[string]bool{}
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		set[name] = true
	}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if hostos.Forwarded(name) && !hostOnly[name] && !set[name] {
			env = append(env, kv)
		}
	}
	return env
}

// workdir is where a forwarded command runs: here, when the VM has this
// directory (anything under the home directory), or else the home directory.
func (v *VM) workdir() string {
	wd, err := os.Getwd()
	if err != nil {
		return v.Home
	}
	if real, err := filepath.EvalSymlinks(wd); err == nil {
		wd = real
	}
	if rel, err := filepath.Rel(v.Home, wd); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
		return wd
	}
	_, _ = fmt.Fprintf(v.Log, "note: the VM only has your home directory, so this runs in %s instead of %s\n", v.Home, wd)
	return v.Home
}

// ForwardArgs is the command line (limactl's, or ssh's for Cloud Hypervisor)
// that runs args as agentbox in the VM.
func (v *VM) ForwardArgs(workdir string, args []string) []string {
	argv := append([]string{"env"}, v.forwardEnv()...)
	argv = append(argv, vmBinary)
	return v.command(workdir, stdinTerminal(), append(argv, args...))
}

// Forward runs args as agentbox in the VM, and becomes that command: limactl
// or ssh replaces this process, so the terminal, Ctrl-C and the exit status
// are the command's own.
func (v *VM) Forward(ctx context.Context, args []string) error {
	if err := v.Ready(ctx); err != nil {
		return err
	}
	return execve(v.ForwardArgs(v.workdir(), args))
}

// execve becomes argv, finding its program on PATH when it isn't a path.
func execve(argv []string) error {
	prog := argv[0]
	if !strings.ContainsRune(prog, os.PathSeparator) {
		p, err := exec.LookPath(prog)
		if err != nil {
			return err
		}
		prog = p
	}
	return syscall.Exec(prog, argv, os.Environ())
}

// stdinTerminal reports whether this command was given a terminal, which a
// Cloud Hypervisor VM's command is given too (ssh -t).
func stdinTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// command is the command line that runs argv in the VM as its user, in
// workdir; tty asks for a terminal, which limactl gives whenever it has one.
func (v *VM) command(workdir string, tty bool, argv []string) []string {
	if v.CHV != nil {
		return chvSSHArgs(v.CHV.Config, v.CHV.Layout, v.CHV.Self, workdir, tty, argv)
	}
	return append([]string{v.Limactl, "shell", "--workdir", workdir, v.Name, "--"}, argv...)
}

// exec runs argv in the VM, with stdin, and returns what it printed.
func (v *VM) exec(ctx context.Context, stdin io.Reader, argv ...string) (string, error) {
	if v.CHV != nil {
		return v.CHV.exec(ctx, v, stdin, argv)
	}
	return v.lima(ctx, stdin, append([]string{"shell", v.Name, "--"}, argv...)...)
}

// execLog runs argv in the VM with its output going to the log, for the steps
// that take long enough to want it.
func (v *VM) execLog(ctx context.Context, argv ...string) error {
	if v.CHV != nil {
		return v.CHV.execLog(ctx, v, argv)
	}
	return v.limaLog(ctx, append([]string{"shell", "--workdir", v.Home, v.Name, "--"}, argv...)...)
}

func (v *VM) lima(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, v.Limactl, args...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return stdout.String(), fmt.Errorf("limactl %s: %w", strings.Join(args, " "), err)
		}
		return stdout.String(), fmt.Errorf("limactl %s: %w: %s", args[0], err, lastLine(msg))
	}
	return stdout.String(), nil
}

// limaLog runs limactl with its output going to the log, for the steps that
// take long enough to want it.
func (v *VM) limaLog(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, v.Limactl, args...)
	cmd.Stdout, cmd.Stderr = v.Log, v.Log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("limactl %s: %w", args[0], err)
	}
	return nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
