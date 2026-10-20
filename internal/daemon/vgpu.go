package daemon

import (
	"context"
	"sync"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// vgpuSetup is the readying of a Mac's krunkit VM's GPU (agent.ReadyVenus):
// a kernel with the virtio-gpu driver and the Venus driver the Mac needs,
// which only "GPU for agents" turned on starts, since it changes the VM's
// kernel and takes minutes. The daemon runs it because the setting is the
// daemon's: as it starts with the setting on, which also finishes what a
// restart was waiting for, and when the setting is turned on.
type vgpuSetup struct {
	mu      sync.Mutex
	running bool
	err     string // why the last run failed, "" when it didn't
	wg      sync.WaitGroup
}

// readyVMGPU starts readying the VM's GPU in the background when this is a
// krunkit VM, "GPU for agents" is on, and it isn't under way already; when
// it's done, agents get the GPU. retry is for the setting being turned on:
// it tries again a kernel that didn't boot.
func (s *Server) readyVMGPU(ctx context.Context, retry bool) {
	if !agent.VenusVM() {
		return
	}
	if on, err := s.store.Flag(ctx, state.SettingAgentGPU); err != nil || !on {
		return
	}
	v := &s.vgpu
	v.mu.Lock()
	if v.running {
		v.mu.Unlock()
		return
	}
	v.running, v.err = true, ""
	v.wg.Add(1)
	v.mu.Unlock()
	go func() {
		defer v.wg.Done()
		err := agent.ReadyVenus(ctx, retry, s.cfg.Log)
		v.mu.Lock()
		v.running = false
		if err != nil {
			v.err = err.Error()
		}
		v.mu.Unlock()
		if err != nil {
			s.logf("readying the VM's GPU for agents: %v", err)
			return
		}
		if changed, err := s.manager(nil).ApplyGPU(ctx); err != nil {
			s.logf("giving agents the VM's GPU: %v", err)
		} else if changed > 0 {
			s.logf("gave %d agent machine(s) the VM's GPU", changed)
		}
	}()
}

// vmGPU is the VM's GPU for Settings, with how far readying it has got in a
// krunkit VM whose GPU isn't ready.
func (s *Server) vmGPU() api.VMGPU {
	gpu := agent.FindVMGPU()
	out := api.VMGPU{Kind: gpu.Kind, Node: gpu.Node, Reason: gpu.Reason}
	if gpu.Kind != agent.GPUNone || !agent.VenusVM() {
		return out
	}
	s.vgpu.mu.Lock()
	defer s.vgpu.mu.Unlock()
	switch {
	case s.vgpu.running:
		out.Setup = api.VMGPUSetupRunning
	case s.vgpu.err != "":
		out.Setup, out.Reason = api.VMGPUSetupFailed, s.vgpu.err
	case agent.VenusRestart():
		out.Setup = api.VMGPUSetupRestart
	default:
		out.Setup = api.VMGPUSetupAvailable
	}
	return out
}
