// Package hostvm runs AgentBox on a machine that isn't Linux. Nothing of
// AgentBox is ported: the daemon, Incus and every agent run in a Linux VM made
// with Lima, exactly as they run on a Linux machine, and the agentbox command
// on the Mac is a front end for that VM.
//
//   - `agentbox vm …` makes, starts, stops and removes the VM (cmd.go).
//   - Every other command runs in the VM, in the same working directory, with
//     the terminal, the exit status and Ctrl-C of a local one (Forward).
//   - The daemon's unix socket is forwarded by Lima to where the Mac's app and
//     command look for it, so the app talks to it as to a local daemon (D19).
//   - The Mac's home directory is shared into the VM at the same path, so a
//     project keeps its path, which is what AgentBox mounts into each agent.
//     The daemon's state stays on the VM's own disk; agents' worktrees go on
//     the share, where the Mac's editors can open them (paths.Worktrees).
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

	"agentbox/internal/hostos"
	"agentbox/internal/paths"
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
// AgentBox itself: always on macOS, and on Linux when AGENTBOX_FRONT_END=vm,
// which is how the VM is tested without a Mac.
func Front() bool {
	return runtime.GOOS == "darwin" || os.Getenv("AGENTBOX_FRONT_END") == "vm"
}

// VM is AgentBox's Lima instance, seen from the host.
type VM struct {
	Limactl string // the limactl binary
	Name    string // the Lima instance
	// Home is the host user's home directory, shared into the VM at the same
	// path.
	Home string
	// Paths are the host's AgentBox paths: the socket Lima forwards the
	// daemon's to, and the worktrees directory on the share.
	Paths paths.Paths
	// Binary is the Linux agentbox kept in the VM.
	Binary string
	// VMType is Lima's vmType; "" is its default (vz on a Mac). The Linux
	// tests set qemu through AGENTBOX_VM_TYPE.
	VMType string
	// Log is where progress goes: the front end's stderr.
	Log io.Writer
}

// New finds limactl and the Linux binary, and describes the VM for this user.
func New() (*VM, error) {
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
		}
		if err := dec.Decode(&inst); errors.Is(err, io.EOF) {
			return State{}, nil
		} else if err != nil {
			return State{}, fmt.Errorf("reading limactl list: %w", err)
		}
		if inst.Name == v.Name {
			return State{Exists: true, Status: inst.Status, Dir: inst.Dir, CPUs: inst.CPUs, Memory: inst.Memory, Disk: inst.Disk, Arch: inst.Arch}, nil
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
	cpus := min(max(runtime.NumCPU()/2, 2), 8)
	return Size{CPUs: cpus, Memory: "8GiB", Disk: "100GiB"}
}

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
	fmt.Fprintf(v.Log, "==> Making the VM %s (%d CPUs, %s, %s disk) from %s\n", v.Name, size.CPUs, size.Memory, size.Disk, file)
	return v.limaLog(ctx, "create", "--tty=false", "--name", v.Name, file)
}

func (v *VM) Start(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(v.Paths.Socket()), 0o700); err != nil {
		return err
	}
	fmt.Fprintf(v.Log, "==> Starting AgentBox's VM (%s)\n", v.Name)
	return v.limaLog(ctx, "start", "--tty=false", v.Name)
}

func (v *VM) Stop(ctx context.Context) error {
	return v.limaLog(ctx, "stop", v.Name)
}

func (v *VM) Delete(ctx context.Context) error {
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
		return fmt.Errorf("Lima says AgentBox's VM is broken: see limactl list, and %s", filepath.Join(st.Dir, "ha.stderr.log"))
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
			return func() { f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("locking AgentBox's VM: %w", err)
		}
		if !waited {
			if exclusive {
				fmt.Fprintln(v.Log, "==> Waiting for the agentbox commands using the VM to finish with it")
			} else {
				fmt.Fprintln(v.Log, "==> Waiting for AgentBox's VM: it's being resized")
			}
		}
		select {
		case <-ctx.Done():
			f.Close()
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
	defer f.Close()
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
	have, _ := v.lima(ctx, nil, "shell", v.Name, "--", "sh", "-c", "sha256sum "+vmBinary+" 2>/dev/null | cut -d' ' -f1")
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
	defer f.Close()
	fmt.Fprintf(v.Log, "==> Installing %s in the VM as %s\n", v.Binary, vmBinary)
	script := fmt.Sprintf(`set -e; t=%[1]s.new.$$; cat >"$t"; chmod 0755 "$t"; mv -f "$t" %[1]s`, vmBinary)
	_, err = v.lima(ctx, f, "shell", v.Name, "--", "sudo", "sh", "-c", script)
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

// bridgeSubnet is the Incus bridge's address and subnet in the VM.
const bridgeSubnet = "10.87.0.1/24"

// Setup is everything `agentbox vm init` does after the VM runs: AgentBox's
// binary, the same host setup a Linux machine gets (Incus, its btrfs pool, the
// bridge, the user mapping), the host's git identity, the host's settings for
// the VM's login shells, and the daemon.
func (v *VM) Setup(ctx context.Context) error {
	if err := v.Install(ctx); err != nil {
		return err
	}
	// Lima names the VM's user after the host's, unless that name won't do
	// on Linux: ask the VM rather than assume.
	guest, err := v.lima(ctx, nil, "shell", v.Name, "--", "id", "-un")
	if err != nil {
		return err
	}
	fmt.Fprintln(v.Log, "==> Host setup in the VM: Incus, its storage and network, and the user mapping")
	// Incus can't pick the bridge's subnet here: it rules out any subnet where
	// an address answers a ping, and Lima's user-mode network answers them all.
	// The VM's only network is Lima's 192.168.5.0/24, so a fixed one is safe.
	if err := v.shellLog(ctx, "sudo", vmBinary, "host", "setup", "--user", strings.TrimSpace(guest), "--bridge-subnet", bridgeSubnet); err != nil {
		return fmt.Errorf("host setup in the VM: %w", err)
	}
	// The VM's user has a home of its own; the daemon reads git's identity
	// from there, for the commits it makes (snapshots, the agents' identity).
	if _, err := os.Stat(filepath.Join(v.Home, ".gitconfig")); err == nil {
		link := fmt.Sprintf(`[ -e ~/.gitconfig ] && [ ! -L ~/.gitconfig ] || ln -sfn %s ~/.gitconfig`, shellQuote(filepath.Join(v.Home, ".gitconfig")))
		if _, err := v.lima(ctx, nil, "shell", v.Name, "--", "sh", "-c", link); err != nil {
			return fmt.Errorf("linking your git identity into the VM: %w", err)
		}
	}
	// A daemon started by hand in the VM, without the front end's settings,
	// would put new worktrees on the VM's disk instead of the share.
	script := `cat >/etc/profile.d/agentbox-host.sh`
	if _, err := v.lima(ctx, strings.NewReader(v.profile()), "shell", v.Name, "--", "sudo", "sh", "-c", script); err != nil {
		return fmt.Errorf("writing the host's settings into the VM: %w", err)
	}
	fmt.Fprintln(v.Log, "==> Starting the daemon")
	return v.shellLog(ctx, append([]string{"env"}, append(v.forwardEnv(), vmBinary, "daemon", "start")...)...)
}

// hostOnly are the AGENTBOX_ settings that describe the host's side: the
// front end's own, and paths that only mean something on the host.
var hostOnly = map[string]bool{
	"AGENTBOX_FRONT_END": true, "AGENTBOX_VM": true, "AGENTBOX_VM_TYPE": true,
	"AGENTBOX_LIMACTL": true, "AGENTBOX_LINUX_BINARY": true, "AGENTBOX_BIN": true,
	"AGENTBOX_SOCKET": true, "AGENTBOX_WORKTREES": true, hostos.Env: true, hostos.HomeEnv: true,
}

// vmEnv is what the VM's agentbox has to be told about the host whoever runs
// it: the host's OS, its home directory, and where the worktrees go.
func (v *VM) vmEnv() []string {
	return []string{
		hostos.Env + "=" + runtime.GOOS,
		hostos.HomeEnv + "=" + v.Home,
		"AGENTBOX_WORKTREES=" + v.Paths.Worktrees(),
	}
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

// forwardEnv is what every command in the VM is told about the host: vmEnv,
// and every other AGENTBOX_ setting of the host's (AGENTBOX_ENV,
// AGENTBOX_PREVIEW_ADDR, AGENTBOX_IMAGE_URL…), which mean the same in the VM.
func (v *VM) forwardEnv() []string {
	env := v.vmEnv()
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if hostos.Forwarded(name) && !hostOnly[name] {
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
	fmt.Fprintf(v.Log, "note: the VM only has your home directory, so this runs in %s instead of %s\n", v.Home, wd)
	return v.Home
}

// ForwardArgs is the limactl command line that runs args as agentbox in the VM.
func (v *VM) ForwardArgs(workdir string, args []string) []string {
	argv := []string{v.Limactl, "shell", "--workdir", workdir, v.Name, "--", "env"}
	argv = append(argv, v.forwardEnv()...)
	argv = append(argv, vmBinary)
	return append(argv, args...)
}

// Forward runs args as agentbox in the VM, and becomes that command: limactl
// replaces this process, so the terminal, Ctrl-C and the exit status are the
// command's own.
func (v *VM) Forward(ctx context.Context, args []string) error {
	if err := v.Ready(ctx); err != nil {
		return err
	}
	argv := v.ForwardArgs(v.workdir(), args)
	return syscall.Exec(argv[0], argv, os.Environ())
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

func (v *VM) shellLog(ctx context.Context, args ...string) error {
	return v.limaLog(ctx, append([]string{"shell", "--workdir", v.Home, v.Name, "--"}, args...)...)
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
