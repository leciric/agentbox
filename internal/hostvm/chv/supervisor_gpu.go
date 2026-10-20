package chv

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"agentbox/internal/api"
)

// kernelInstallFailed starts GPUStatus.KernelFailed when the generic kernel
// couldn't be installed, rather than didn't boot.
const kernelInstallFailed = "install: "

const (
	// gpuPoll is how often the supervisor asks the daemon for "GPU for
	// agents".
	gpuPoll = 15 * time.Second
	// kernelInstallTimeout bounds installing the generic kernel in the VM.
	kernelInstallTimeout = 15 * time.Minute
)

// kernelTrialTimeout is how long a kernel booted on trial has to come up (ssh
// answers) before the VM is started again on its default one, and
// trialPoll how often it's asked: variables for tests.
var (
	kernelTrialTimeout = 5 * time.Minute
	trialPoll          = 5 * time.Second
)

// gpuLoop keeps the VM's GPU as "GPU for agents" says, while the VM runs
// (gpu.go): it reads the switch from the daemon, readies the kernel when it's
// on, sees a kernel on trial come up or starts the VM again without it, and
// says what it found in gpu.json and to the daemon.
func (s *supervisor) gpuLoop(ctx context.Context, m *chMachine) {
	st := ReadGPUStatus(s.l)
	if st.Trial != "" && !s.trialCameUp(ctx, m, &st) {
		return
	}
	g := gpuSettler{st: st, plan: m.gpuPlan, attached: m.gpu != nil}
	tick := time.NewTicker(gpuPoll)
	defer tick.Stop()
	for {
		var settings api.Settings
		if err := daemonJSON(ctx, s.daemonClient(10*time.Second), http.MethodGet, "/v1/settings", &settings); err == nil {
			s.settleGPU(ctx, m, &g, settings.AgentGPU)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// trialCameUp waits for a VM booted on a kernel on trial to come up, and
// otherwise ends it and starts it again: GRUB then boots its default kernel,
// since the trial was for one boot. It reports whether the loop goes on.
func (s *supervisor) trialCameUp(ctx context.Context, m *chMachine, st *GPUStatus) bool {
	deadline := time.Now().Add(kernelTrialTimeout)
	for time.Now().Before(deadline) {
		if out, err := s.ssh(ctx, "uname -r"); err == nil {
			running := firstLine(out)
			if running != st.Trial {
				// GRUB didn't boot it: the cloud kernel runs.
				s.logf("the VM booted %s, not the generic kernel %s it was to try", running, st.Trial)
				st.KernelFailed = st.Trial
			}
			st.Trial = ""
			_ = writeGPUStatus(s.l, *st)
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(trialPoll):
		}
	}
	s.logf("the VM didn't come up on the generic kernel %s within %s: starting it again on its cloud kernel", st.Trial, kernelTrialTimeout)
	st.KernelFailed, st.Trial = st.Trial, ""
	if err := writeGPUStatus(s.l, *st); err != nil {
		s.logf("saving %s: %v", s.l.GPUFile(), err)
		return false
	}
	s.mu.Lock()
	s.restartVM = true
	s.mu.Unlock()
	m.halt()
	return false
}

// gpuSettler is what gpuLoop knows between its rounds.
type gpuSettler struct {
	st       GPUStatus
	plan     gpuPlan // what the VM started with
	attached bool    // whether it has the GPU
	// kernel is whether the kernel was seen to this boot: ready, on trial
	// at the next start, or failed; driver whether it has virtio-gpu.
	kernel, driver bool
	published      string // the status the guest has, as JSON
}

// settleGPU is one round of gpuLoop, with want the switch.
func (s *supervisor) settleGPU(ctx context.Context, m *chMachine, g *gpuSettler, want bool) {
	if want != g.st.Want {
		s.logf(`"GPU for agents" is %s`, map[bool]string{true: "on", false: "off"}[want])
		if !want {
			// Turning it off and on again is how a kernel that failed is
			// tried again.
			g.st.KernelFailed = ""
		}
		g.kernel = false
	}
	g.st.Want = want
	if want && !g.kernel && g.st.KernelFailed == "" {
		g.kernel = true
		s.readyKernel(ctx, g)
	}
	g.st = gpuState(g.st, gpuFacts{
		attached:   g.attached,
		// crosvm exits when Cloud Hypervisor does (one per VM start): only
		// while the VM runs is that a failure.
		crosvmGone: g.attached && m.gpu != nil && m.gpu.exited() && m.running(),
		driver:     g.driver,
		started:    g.plan,
		now:        planGPU(true, findRenderNodes(), os.Getenv("AGENTBOX_VM_RENDER_NODE"), openRenderNode),
	})
	s.publishGPU(ctx, g)
}

// readyKernel has the VM's kernel ready for the GPU: the generic one
// installed and on trial at the next start when the cloud one runs, and the
// newest generic made the default when it runs.
func (s *supervisor) readyKernel(ctx context.Context, g *gpuSettler) {
	out, err := s.sshFor(ctx, defaultKernelScript, time.Minute)
	if err != nil {
		s.logf("making the VM's generic kernel its default: %v", err)
		return
	}
	if firstLine(out) != "cloud" {
		g.driver = true // the generic kernel runs, and is the default
		return
	}
	s.logf("installing Debian's generic kernel in the VM, which has the GPU driver its cloud kernel lacks")
	g.st.Reason = "AgentBox's VM is installing a kernel with the GPU driver, in the background."
	g.st.State = GPURestart
	s.publishGPU(ctx, g)
	out, err = s.sshFor(ctx, kernelScript, kernelInstallTimeout)
	if err != nil {
		s.logf("installing the generic kernel in the VM: %v", err)
		g.st.KernelFailed = kernelInstallFailed + err.Error()
		return
	}
	if firstLine(out) == "driver" {
		g.driver = true
	} else if v, ok := strings.CutPrefix(firstLine(out), "trial "); ok {
		g.st.Trial = v
		s.logf("the VM boots the generic kernel %s once at its next start, and keeps it if it comes up", v)
	}
	_ = writeGPUStatus(s.l, g.st)
}

// gpuFacts are what gpuState decides from.
type gpuFacts struct {
	attached   bool // the VM started with the GPU
	crosvmGone bool // and crosvm has exited since
	driver     bool // the VM's kernel has virtio-gpu, as far as is known
	started    gpuPlan
	now        gpuPlan // what the VM would get if it started now
}

// gpuState is the status for st's switch, from f.
func gpuState(st GPUStatus, f gpuFacts) GPUStatus {
	attached, started, now := f.attached, f.started, f.now
	st.State, st.Reason, st.Node, st.GPU = GPUNone, "", "", ""
	st.Vulkan = ""
	if attached {
		st.Node, st.GPU = started.node.Path, started.node.name()
		if started.node.Vendor != "" && started.node.Device != "" {
			st.Vulkan = started.node.Vendor + ":" + started.node.Device
		}
	} else if now.ok() {
		st.Node, st.GPU = now.node.Path, now.node.name()
	}
	const restart = "restart AgentBox's VM for agents to get it (agentbox vm stop, then agentbox vm start; agents stop with it)"
	const retry = `turn "GPU for agents" off and on to try again`
	switch {
	case attached && f.crosvmGone:
		st.Reason = "the VM's GPU device (crosvm) stopped: " + restart + "."
	case attached && st.Want && f.driver:
		st.State = GPUOn
	case attached && !st.Want:
		// The VM keeps the device until it restarts; agents don't get it.
		st.State = GPUOff
	case st.Want && strings.HasPrefix(st.KernelFailed, kernelInstallFailed):
		st.Reason = "installing Debian's generic kernel, which has the GPU driver, in the VM failed (" + strings.TrimPrefix(st.KernelFailed, kernelInstallFailed) + "): " + retry + "."
	case st.Want && st.KernelFailed != "":
		st.Reason = "the VM didn't come up on Debian's generic kernel " + st.KernelFailed + ", which has the GPU driver, so it runs its cloud kernel, which hasn't: " + retry + "."
	case st.Want && st.Trial != "":
		st.State = GPURestart
		st.Reason = "the VM has a kernel with the GPU driver now: " + restart + "."
	case attached:
		st.Reason = "the VM's kernel has no GPU driver."
	case !now.ok():
		st.Reason = now.reason + "."
	case st.Want && started.reason != gpuOff:
		// It was on as the VM started, and the GPU failed.
		st.Reason = started.reason + "."
	case st.Want:
		st.State = GPURestart
		st.Reason = "the VM gets this GPU when it next starts: " + restart + "."
	default:
		st.State = GPUOff
	}
	return st
}

// publishGPU saves the status in gpu.json and gives it to the daemon, when
// it changed.
func (s *supervisor) publishGPU(ctx context.Context, g *gpuSettler) {
	b := guestStatusScript(g.st)
	if b == g.published {
		return
	}
	if err := writeGPUStatus(s.l, g.st); err != nil {
		s.logf("saving %s: %v", s.l.GPUFile(), err)
	}
	if _, err := s.ssh(ctx, b); err != nil {
		s.logf("telling the VM about its GPU: %v", err)
		return
	}
	g.published = b
	if g.st.Reason != "" {
		s.logf("GPU: %s: %s", g.st.State, g.st.Reason)
	}
}
