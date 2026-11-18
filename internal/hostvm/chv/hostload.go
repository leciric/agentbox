package chv

import (
	"context"
	"net"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/hostload"
)

// hostLoadLoop tells the daemon how loaded this computer is, every
// hostload.Interval while the VM runs, so it holds heavy commands back while
// the desktop needs the machine (internal/hostload). The VM's memory is this
// supervisor's to size (memory.go), so the daemon isn't asked to drop caches;
// it is told instead what the VM may grow to now and whether boots should
// wait (withMemory). What hostload judges is kept for the memory loop too,
// which doesn't grow the VM while the host is busy (hostBusyFor).
func (s *supervisor) hostLoadLoop(ctx context.Context) {
	send := hostload.HTTPPut(func(ctx context.Context) (net.Conn, error) {
		return dialVsock(ctx, s.l.VsockSocket(), PortDaemon)
	})
	put := func(ctx context.Context, load api.HostLoad) error {
		s.mu.Lock()
		s.noteHostLoad(load)
		ready := s.daemonReady && s.state != api.VMPaused
		load = s.withMemory(load)
		s.mu.Unlock()
		if !ready {
			return nil
		}
		return send(ctx, load)
	}
	hostload.Report(ctx, put, false, func(l api.HostLoad) {
		if l.Busy {
			s.logf("host load: this computer is busy (%s): the daemon holds heavy commands back", l.Why)
		} else {
			s.logf("host load: this computer is calm again")
		}
	})
}

// noteHostLoad keeps why load is busy, through its calm-down (when Why is
// ""), for hostBusyFor. With s.mu held.
func (s *supervisor) noteHostLoad(load api.HostLoad) {
	switch {
	case !load.Busy:
		s.hostBusy = ""
	case load.Why != "":
		s.hostBusy = load.Why
	}
	s.hostBusyAt = load.At
}

// hostBusyFor is memSample.HostBusy at now: why the host is busy, when that is
// memory or its disk and the reading is fresh. A busy CPU doesn't count: the
// VM's own vCPUs make most of it while agents build. With s.mu held.
func (s *supervisor) hostBusyFor(now time.Time) string {
	if now.Sub(s.hostBusyAt) > 2*hostload.Interval+time.Second {
		return ""
	}
	switch s.hostBusy {
	case "memory", "io":
		return s.hostBusy
	}
	return ""
}

// withMemory adds what the memory loop knows to load: the most the VM may
// grow to now, which the daemon counts boots and builds against instead of
// the cap, and whether the host has been short of memory lately, which
// holds every boot. With s.mu held.
func (s *supervisor) withMemory(load api.HostLoad) api.HostLoad {
	if s.sample == nil {
		return load
	}
	load.VMCeiling = s.mem.Ceiling
	load.HoldBoots = s.mem.HoldBoots
	return load
}
