package daemon

import (
	"context"
	"time"

	"agentbox/internal/bridgecheck"
	"agentbox/internal/incus"
)

// checkBridge makes sure containers on the Incus bridge reach the internet;
// the tests' daemons must not change the developer's bridge.
var checkBridge = func(ctx context.Context, inc incus.Client) (bridgecheck.Result, error) {
	return bridgecheck.Check(ctx, bridgecheck.System(inc))
}

// bridgeWait is how long checkBridgeOnce waits for Incus to answer.
const bridgeWait = 10 * time.Minute

// checkBridgeOnce makes sure, once Incus answers, that containers on its
// bridge resolve, connect and download (bridgecheck): on WSL the bridge's DNS,
// IPv6 or MTU can fail while the distro's own work, and the distro may be on
// another network than at the last start. Once per start, not a loop: what it
// sets, the bridge keeps.
func (s *Server) checkBridgeOnce(ctx context.Context) {
	if s.incus.every == 0 {
		return // tests drive Incus
	}
	for deadline := time.Now().Add(bridgeWait); ; {
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
	res, err := checkBridge(ctx, s.cfg.Incus)
	switch {
	case err != nil && ctx.Err() == nil:
		s.logf("could not check %s: %v", bridgecheck.Bridge, err)
	case res.Message != "":
		s.logf("%s", res.Message)
	}
}
