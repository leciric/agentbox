package api

import "time"

// AgentBox can run all of itself — the daemon, Incus and every agent — in one
// Linux VM, with the host's app and agentbox command as its front end: always
// on a Mac (Lima), and on Linux when the user chose it at setup (Cloud
// Hypervisor, internal/hostvm/chv). The daemon is in the VM, so it can't say
// whether the VM runs: these types are served by the host's side instead.
//
// The contract, for the app's top bar and "Free resources"
// (desktop/src/main/vmpower.ts):
//
//   - `agentbox vm power --json` prints what the top bar shows:
//     {state, memoryUsed, memoryGranted, memoryCap, cpus, error?,
//     pausedForDisk?, hostFree?}, or
//     {"mode":"host"} when this installation doesn't run in a VM. The app's
//     main process serves it to the renderer as IPC vm:power.
//   - `agentbox vm start|pause|resume|stop` each return once the VM is in its
//     new state, start and resume only once the daemon inside answers, and on
//     failure exit non-zero with why on the last line of stderr (IPC vm:act).
//   - "Free resources" is POST /v1/agents/stop (`agentbox stop --all`), which
//     stops every running agent so none comes back when the VM next starts,
//     then `agentbox vm stop`, which powers the VM off and gives all of its
//     memory back.
//   - `agentbox vm status --json` prints the whole VMStatus below, in either
//     mode ("host" when there's no VM; on a Mac, Lima's older shape). While
//     the Cloud Hypervisor VM runs, its supervisor also serves it as
//     GET /v1/vm on paths.VMSocket, with POST /v1/vm/pause, /v1/vm/resume and
//     /v1/vm/stop (a VMStopRequest); nothing there means the VM is off.

// Modes of an installation: where the daemon, Incus and the agents run.
const (
	ModeHost = "host" // on this machine itself (Linux host setup)
	ModeVM   = "vm"   // in a VM on this machine, with this machine as its front end
)

// Drivers of the VM.
const (
	VMDriverLima            = "lima"
	VMDriverCloudHypervisor = "cloud-hypervisor"
	// VMDriverVZ is Apple's Virtualization framework, driven by AgentBox
	// itself rather than Lima, on a Mac: experimental.
	VMDriverVZ = "vz"
)

// States of the VM.
const (
	VMOff      = "off"      // not running: it holds no memory or CPU
	VMStarting = "starting" // booting, or its daemon isn't up yet
	VMRunning  = "running"
	VMPaused   = "paused" // frozen in memory: no CPU, same memory, resumes in milliseconds
	VMStopping = "stopping"
	VMMissing  = "missing" // not made yet: `agentbox vm init`
)

// VMStatus is AgentBox's VM as the host sees it.
type VMStatus struct {
	Mode   string `json:"mode"`   // ModeHost or ModeVM
	Driver string `json:"driver"` // VMDriverLima, VMDriverCloudHypervisor or VMDriverVZ; "" in ModeHost
	Name   string `json:"name,omitempty"`
	State  string `json:"state"` // VMOff, VMStarting, VMRunning, VMPaused, VMStopping or VMMissing
	// Since is when it got to State, when that's known.
	Since time.Time `json:"since"`
	// Problem is why there's no VM to use, and what to run about it.
	Problem string   `json:"problem,omitempty"`
	CPUs    int      `json:"cpus,omitempty"`
	Memory  VMMemory `json:"memory"`
	Disk    VMDisk   `json:"disk"`
	// Limits are the sizes `agentbox vm resize` takes for a Cloud Hypervisor
	// VM on this machine: 1 CPU to every core, a memory cap from what the
	// VM boots with to all of the host's memory, and a pool disk from the
	// size it has (it only grows) to VMMaxDisk. Missing for Lima's.
	Limits *VMLimits `json:"limits,omitempty"`
	// Live is what the running VM can be resized to without a restart: the
	// CPUs it can hotplug and the memory its virtio-mem region holds, both
	// fixed when it booted, and its pool disk when it can grow while it runs
	// (Cloud Hypervisor's can, the vz driver's can't: MaxDisk is 0). Missing
	// when it isn't running.
	Live *VMLimits `json:"live,omitempty"`
	// PausedForDisk says the supervisor paused the VM because the host's disk
	// that holds its disk images got down to its last VMDiskBackstop bytes
	// free: the VM's disks are sparse, so they grow into whatever the host
	// has, and a write the host can't take would corrupt them. It's resumed
	// once the host has twice that free again.
	PausedForDisk bool `json:"pausedForDisk,omitempty"`
}

// VMDiskBackstop is the free space on the host's disk below which the
// supervisor pauses the VM (VMStatus.PausedForDisk): the last line, under
// the daemon's own disk guard in the VM, for when that one can't act.
const VMDiskBackstop = int64(2) << 30

// VMMaxDisk is the largest pool disk `agentbox vm resize --disk` makes. The
// disk is sparse, so its size is only what it may grow to on the host's disk.
const VMMaxDisk = int64(16) << 40

// VMLimits bound a VM's CPUs, memory and pool disk (bytes). The disk's are 0
// where it can't be resized.
type VMLimits struct {
	MinCPUs   int   `json:"minCpus"`
	MaxCPUs   int   `json:"maxCpus"`
	MinMemory int64 `json:"minMemory"`
	MaxMemory int64 `json:"maxMemory"`
	MinDisk   int64 `json:"minDisk,omitempty"`
	MaxDisk   int64 `json:"maxDisk,omitempty"`
}

// VMResizeRequest is POST /v1/vm/resize, which the VM's supervisor does
// without a restart when it fits in VMStatus.Live; zero leaves one as it is.
type VMResizeRequest struct {
	CPUs      int   `json:"cpus,omitempty"`
	MemoryCap int64 `json:"memoryCap,omitempty"`
	// Disk is the pool disk's new size, which may only grow: the disk image
	// grows, the guest sees the bigger disk and its btrfs pool is grown to it.
	Disk int64 `json:"disk,omitempty"`
}

// VMMemory is the VM's memory, in bytes. A Cloud Hypervisor VM starts at Min
// and is granted more (virtio-mem) as agents start, up to Cap, and gives it
// back as they stop; a Lima VM has a fixed Granted, and Min = Cap = Granted.
type VMMemory struct {
	Min     int64 `json:"min"`     // what it boots with, and never goes below
	Cap     int64 `json:"cap"`     // the most it may be granted, as the user set it
	Granted int64 `json:"granted"` // what it may use now
	// Used is what the VM's programs use, without its page cache: what it
	// would need if it gave back everything it could.
	Used int64 `json:"used"`
	// Resident is what the VM holds of the host's memory right now: what
	// stopping it would give back. 0 when it's off or unknown.
	Resident int64 `json:"resident"`
}

// VMDisk is the VM's disk images, in bytes, as the host sees them. Each has
// two sizes, never to be mixed: Size is what the VM sees, the image's
// apparent size as the VM's disk settings made it, and Allocated is what it
// really takes on the host's disk. The images are sparse: they start near
// nothing, grow as the VM writes, and only shrink when the VM's discards
// reach them, so Allocated can be more than what the VM says it uses.
type VMDisk struct {
	Size      int64 `json:"size"`      // Pool.Size + Root.Size
	Allocated int64 `json:"allocated"` // Pool.Allocated + Root.Allocated
	// Pool holds Incus's storage pool, every agent's machine and saved base;
	// Root is the VM's own system disk. A Lima VM has one disk for both, in
	// Root, and no Pool. Pool.Size is also what `agentbox vm resize --disk`
	// grows.
	Pool VMDiskImage `json:"pool"`
	Root VMDiskImage `json:"root"`
	// HostFree is what's free on the host's disk that holds the VM's disk
	// images: all they can still grow into.
	HostFree int64 `json:"hostFree,omitempty"`
}

// VMDiskImage is one of the VM's disk images: Size is its apparent size,
// what the VM sees, and Allocated the bytes it takes on the host's disk.
type VMDiskImage struct {
	Size      int64 `json:"size"`
	Allocated int64 `json:"allocated"`
}

// Add counts img in d's totals.
func (d *VMDisk) Add(img VMDiskImage) {
	d.Size += img.Size
	d.Allocated += img.Allocated
}

// VMHomeDisk is what AgentBox keeps in the host's home, outside the VM's disk
// images, measured on the host (`agentbox vm disk --json`): the VM shares
// that home, so neither is in VMDisk. Bytes allocated on the host's disk.
type VMHomeDisk struct {
	Worktrees int64 `json:"worktrees"`
	Media     int64 `json:"media"`
}

// VMStopRequest is POST /v1/vm/stop.
type VMStopRequest struct {
	// Agents stops every running agent through the daemon before the VM
	// powers off, so they stay stopped when it next starts.
	Agents bool `json:"agents,omitempty"`
}
