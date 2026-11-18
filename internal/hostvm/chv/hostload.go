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
// supervisor's to size (memory.go), so the daemon isn't asked to drop caches:
// the report says what the memory loop can give back instead (reclaimable),
// which it does while the host has less than its reserve.
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
	hostload.Report(ctx, put, hostload.Front{Reclaimable: s.reclaimable, OnChange: hostload.LogChange(s.logf)})
}

// reclaimable is what the VM can give back to the host now, by the memory
// loop's last sample; 0 when it has none from the last memSampleFresh.
func (s *supervisor) reclaimable() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sample == nil || time.Since(s.sampleAt) > memSampleFresh {
		return 0
	}
	return s.sample.reclaimable()
}

// memSampleFresh is how old a sample may be for reclaimable to go by: a few
// of the memory loop's ticks, which ssh may take a while to answer.
const memSampleFresh = 10 * time.Second
