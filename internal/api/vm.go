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
//     {state, memoryUsed, memoryGranted, memoryCap, cpus, error?}, or
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
	Driver string `json:"driver"` // VMDriverLima or VMDriverCloudHypervisor; "" in ModeHost
	Name   string `json:"name,omitempty"`
	State  string `json:"state"` // VMOff, VMStarting, VMRunning, VMPaused, VMStopping or VMMissing
	// Since is when it got to State, when that's known.
	Since time.Time `json:"since"`
	// Problem is why there's no VM to use, and what to run about it.
	Problem string   `json:"problem,omitempty"`
	CPUs    int      `json:"cpus,omitempty"`
	Memory  VMMemory `json:"memory"`
	Disk    VMDisk   `json:"disk"`
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

// VMDisk is the VM's disk image, in bytes.
type VMDisk struct {
	Size int64 `json:"size"` // what the VM sees
	Used int64 `json:"used"` // what it takes on the host's disk (it's sparse)
}

// VMStopRequest is POST /v1/vm/stop.
type VMStopRequest struct {
	// Agents stops every running agent through the daemon before the VM
	// powers off, so they stay stopped when it next starts.
	Agents bool `json:"agents,omitempty"`
}
