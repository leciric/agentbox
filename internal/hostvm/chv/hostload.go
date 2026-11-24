package chv

import (
	"context"
	"net"

	"agentbox/internal/api"
	"agentbox/internal/hostload"
)

// hostLoadLoop tells the daemon how loaded this computer is, every
// hostload.Interval while the VM runs (internal/hostload). The VM's memory is
// this supervisor's to size (memory.go), so the daemon isn't asked to drop
// caches; it is told instead what the VM may grow to now and whether boots
// should wait (withMemory). Nothing here holds the VM's growth: the host
// being busy doesn't (24 Nov 2026, the user's decision), and what does,
// its memory, the memory loop reads itself (hostMemSample).
func (s *supervisor) hostLoadLoop(ctx context.Context) {
	send := hostload.HTTPPut(func(ctx context.Context) (net.Conn, error) {
		return dialVsock(ctx, s.l.VsockSocket(), PortDaemon)
	})
	put := func(ctx context.Context, load api.HostLoad) error {
		s.mu.Lock()
		ready := s.daemonReady && s.state != api.VMPaused
		load = s.withMemory(load)
		s.mu.Unlock()
		if !ready {
			return nil
		}
		return send(ctx, load)
	}
	hostload.Report(ctx, put, false)
}

// withMemory adds what the memory loop knows to load: the most the VM may
// grow to now, which the agents' shared limit follows instead of the cap,
// and whether the host is below its hard floor now, which holds every boot.
// With s.mu held.
func (s *supervisor) withMemory(load api.HostLoad) api.HostLoad {
	if s.sample == nil {
		return load
	}
	load.VMCeiling = s.mem.Ceiling
	load.HoldBoots = s.mem.HoldBoots
	return load
}
