// Package chv runs AgentBox's Linux VM with Cloud Hypervisor, on a Linux host:
// the daemon, Incus and every agent run in the VM, and the host's app and
// agentbox command are its front end, as on a Mac (internal/hostvm, D92). The
// host needs no Incus, no host setup, no bridge and no root: every program
// that runs the VM is a static binary AgentBox fetches into paths.VM, and
// runs as the user.
//
//   - Cloud Hypervisor runs the VM, from Debian's cloud image (image.go),
//     booted with Cloud Hypervisor's own UEFI firmware.
//   - passt gives it a network (vhost-user), with no tap device or bridge on
//     the host: the VM's traffic leaves as the user's own sockets.
//   - virtiofsd shares the host's home into the VM at the same path, as Lima
//     does on a Mac.
//   - vsock carries the daemon's socket, ssh and the preview proxy from the
//     VM to the host (forward.go); nothing listens on the host's network.
//   - The VM starts small and is given memory with virtio-mem as its agents
//     need it, up to a cap, and gives it back (memory.go).
//
// `agentbox vm run` is the supervisor: one process per running VM that runs
// all of the above and serves the VM's state on paths.VMSocket (api.VMStatus).
package chv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"agentbox/internal/paths"
)

const (
	// DefaultName is the VM's name, AGENTBOX_VM changes it.
	DefaultName = "agentbox"
	// GiB is a gibibyte.
	GiB = int64(1) << 30
	// DefaultMemoryMin is what the VM boots with.
	DefaultMemoryMin = 4 * GiB
	// DefaultDisk is the size of the VM's pool disk, allocated as it's used.
	DefaultDisk = 100 * GiB
	// RootDisk is the size of the VM's own system disk, allocated as it's used.
	RootDisk = 20 * GiB
	// GuestCID is the VM's vsock address.
	GuestCID = 3
)

// The vsock ports the VM listens on (image.go's user-data forwards each to a
// socket in the VM), which the supervisor connects the host's ends to.
const (
	PortSSH     = 22   // the VM's sshd, for commands the front end runs in it
	PortDaemon  = 1024 // the daemon's API socket, in the VM user's home
	PortPreview = 7777 // the daemon's preview proxy, 127.0.0.1:7777 in the VM
)

// The VM's network, as passt gives it: the same whatever network the host is
// on, so the VM needn't notice the host moving between networks. Incus's
// bridge in the VM takes BridgeSubnet.
const (
	GuestAddr    = "10.0.2.15"
	GuestGateway = "10.0.2.2" // the host
	GuestDNS     = "10.0.2.3" // passt forwards it to the host's resolver
	BridgeSubnet = "10.87.0.1/24"
)

// Config is the VM as `agentbox vm init` made it, kept in the host's
// Config/vm/<name>.json. That the file exists is what makes this Linux
// machine a front end: see Exists.
type Config struct {
	Name string `json:"name"`
	CPUs int    `json:"cpus"`
	// MemoryMin is what the VM boots with; MemoryCap is the most it's given.
	MemoryMin int64 `json:"memoryMin"`
	MemoryCap int64 `json:"memoryCap"`
	// Disk is the size of the pool disk that holds Incus's storage pool.
	Disk int64 `json:"disk"`
	// User, UID and GID are the host user's, which the VM's user gets too so
	// the files on the share are theirs on both sides. Home is the host's
	// home, shared into the VM at that path; the VM user's own home is
	// GuestHome, on the VM's disk.
	User      string `json:"user"`
	UID       int    `json:"uid"`
	GID       int    `json:"gid"`
	Home      string `json:"home"`
	GuestHome string `json:"guestHome"`
	// IOLimit caps the VM's disk writes, in bytes a second; 0 is none.
	IOLimit int64     `json:"ioLimit,omitempty"`
	Created time.Time `json:"created"`
}

// ErrNotCreated is a VM `agentbox vm init` hasn't made.
var ErrNotCreated = errors.New("AgentBox's VM isn't set up: run agentbox vm init")

// ConfigFile is where the VM's Config is kept.
func ConfigFile(p paths.Paths, name string) string {
	return filepath.Join(p.Config, "vm", name+".json")
}

// Exists reports whether this machine runs AgentBox in a Cloud Hypervisor VM
// named name: whether `agentbox vm init` made one.
func Exists(p paths.Paths, name string) bool {
	_, err := os.Stat(ConfigFile(p, name))
	return err == nil
}

// Load reads the VM's Config; ErrNotCreated when there's none.
func Load(p paths.Paths, name string) (Config, error) {
	b, err := os.ReadFile(ConfigFile(p, name))
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, ErrNotCreated
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", ConfigFile(p, name), err)
	}
	return c, nil
}

// Save writes the VM's Config.
func (c Config) Save(p paths.Paths) error {
	file := ConfigFile(p, c.Name)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := file + ".new"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// Layout is where a VM's files are on the host, under paths.VM.
type Layout struct {
	Root string // paths.VM
	Name string
}

func NewLayout(p paths.Paths, name string) Layout { return Layout{Root: p.VM(), Name: name} }

// Bin is one of the programs that run the VM (tools.go), shared by every VM.
func (l Layout) Bin(name string) string { return filepath.Join(l.Root, "bin", name) }

// Cache holds downloads, like Debian's cloud image, shared by every VM.
func (l Layout) Cache() string { return filepath.Join(l.Root, "cache") }

// Dir is this VM's own directory. Its disks are in it, made with copy on
// write off (chattr +C on the directory) so a btrfs host doesn't fragment them.
func (l Layout) Dir() string { return filepath.Join(l.Root, l.Name) }

func (l Layout) RootDisk() string { return filepath.Join(l.Dir(), "root.raw") }
func (l Layout) PoolDisk() string { return filepath.Join(l.Dir(), "pool.raw") }
func (l Layout) Seed() string     { return filepath.Join(l.Dir(), "seed.img") }

// Key is the ssh key the front end logs into the VM with; Key()+".pub" is its
// public half, which the VM's user authorizes.
func (l Layout) Key() string { return filepath.Join(l.Dir(), "ssh", "id_ed25519") }

// Run holds the running VM's sockets, pid file and logs.
func (l Layout) Run() string       { return filepath.Join(l.Dir(), "run") }
func (l Layout) APISocket() string { return filepath.Join(l.Run(), "ch.sock") }
func (l Layout) VsockSocket() string {
	return filepath.Join(l.Run(), "vsock.sock")
}
func (l Layout) PasstSocket() string { return filepath.Join(l.Run(), "passt.sock") }
func (l Layout) FSSocket() string    { return filepath.Join(l.Run(), "fs.sock") }
func (l Layout) PIDFile() string     { return filepath.Join(l.Run(), "supervisor.pid") }
func (l Layout) LockFile() string    { return filepath.Join(l.Run(), "supervisor.lock") }
func (l Layout) Log() string         { return filepath.Join(l.Dir(), "vm.log") }
func (l Layout) SerialLog() string   { return filepath.Join(l.Dir(), "serial.log") }
