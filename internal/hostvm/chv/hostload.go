package chv

import (
	"context"
	"net"

	"agentbox/internal/api"
	"agentbox/internal/hostload"
)

// hostLoadLoop tells the daemon how loaded this computer is, every
// hostload.Interval while the VM runs, so it holds heavy commands back while
// the desktop needs the machine (internal/hostload). The VM's memory is this
// supervisor's to size (memory.go), so the daemon isn't asked to drop caches.
func (s *supervisor) hostLoadLoop(ctx context.Context) {
	send := hostload.HTTPPut(func(ctx context.Context) (net.Conn, error) {
		return dialVsock(ctx, s.l.VsockSocket(), PortDaemon)
	})
	put := func(ctx context.Context, load api.HostLoad) error {
		s.mu.Lock()
		ready := s.daemonReady && s.state != api.VMPaused
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
