package hostwsl

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/hostload"
)

// errNoSession is a report skipped because the relay has no session to the
// distro: it isn't up, or the app hasn't asked anything yet.
var errNoSession = errors.New("no session to the distro")

// reportHostLoad tells the distro's daemon how loaded Windows is
// (internal/hostload), every hostload.Interval while the relay runs: the app
// is open. Only over a session the app's own requests opened, so a distro
// that Free resources stopped isn't checked on, let alone started, every two
// seconds. WSL sizes its VM itself, so the daemon is asked to drop its caches
// when Windows is short of memory, which WSL's autoMemoryReclaim
// (wslconfig.go) then hands back.
func (r *Relay) reportHostLoad(ctx context.Context, logf func(string, ...any)) {
	hostload.Report(ctx, hostload.HTTPPut(r.dialExisting), true, func(l api.HostLoad) {
		if l.Busy {
			logf("host load: this computer is busy (%s): the daemon holds heavy commands back", l.Why)
		} else {
			logf("host load: this computer is calm again")
		}
	})
}

// dialExisting opens a stream to the daemon over the relay's session, if it
// has one, without starting one.
func (r *Relay) dialExisting(ctx context.Context) (net.Conn, error) {
	r.mu.Lock()
	s := r.session
	r.mu.Unlock()
	if s == nil || s.IsClosed() {
		return nil, errNoSession
	}
	stream, err := s.Open()
	if err != nil {
		return nil, err
	}
	status := make([]byte, 1)
	_ = stream.SetReadDeadline(time.Now().Add(hostload.Interval))
	if _, err := io.ReadFull(stream, status); err != nil || status[0] != bridgeOK {
		_ = stream.Close()
		if err == nil {
			err = errors.New("the daemon isn't running")
		}
		return nil, err
	}
	_ = stream.SetReadDeadline(time.Time{})
	return stream, nil
}
