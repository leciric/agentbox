package daemon

import (
	"context"
	"time"

	"agentbox/internal/bridgedns"
	"agentbox/internal/incus"
)

// bridgeDNS makes sure the Incus bridge answers DNS; the tests' daemons must
// not change the developer's bridge.
var bridgeDNS = func(ctx context.Context, inc incus.Client) (bridgedns.Result, error) {
	return bridgedns.Ensure(ctx, bridgedns.System(inc))
}

// bridgeDNSWait is how long checkBridgeDNS waits for Incus to answer.
const bridgeDNSWait = 10 * time.Minute

// checkBridgeDNS makes sure, once Incus answers, that its bridge answers DNS:
// on WSL its dnsmasq can't always use the distro's resolver, and the distro
// may have moved to another network since the last start. Once per start, not
// a loop: what it sets, dnsmasq keeps.
func (s *Server) checkBridgeDNS(ctx context.Context) {
	if s.incus.every == 0 {
		return // tests drive Incus
	}
	for deadline := time.Now().Add(bridgeDNSWait); ; {
		probe, cancel := context.WithTimeout(ctx, s.incus.probeTimeout)
		err := s.cfg.Incus.Probe(probe)
		cancel()
		if err == nil {
			break
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
	res, err := bridgeDNS(ctx, s.cfg.Incus)
	switch {
	case err != nil && ctx.Err() == nil:
		s.logf("could not check %s's DNS: %v", bridgedns.Bridge, err)
	case res.Message != "":
		s.logf("%s", res.Message)
	}
}
