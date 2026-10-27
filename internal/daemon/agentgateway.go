package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"agentbox/internal/gateway"
)

// The agent gateway is the way to an agent whose machine is a pod: the pod opens
// one WebSocket to this daemon and keeps it, and every operation travels back
// over it (package gateway). Its route is on the daemon's own API, and
// Config.GatewayAddr (AGENTBOX_GATEWAY_ADDR, for `agentbox daemon`) serves it on
// a port of its own as well, for a cluster on this machine, which has no route
// to the tailnet.

// agentGatewayPath is where a pod dials. It is the gateway package's own Path,
// written here because a later change moves the route into that package; until
// then this constant is the one the daemon serves.
const agentGatewayPath = "/v1/agent-gateway"

// agentGatewayAuth is the agent a pod's token belongs to: the token this daemon
// wrote into the pod's Secret, which the store holds only as a hash, so a token
// that was never issued is refused without the database ever reading one back.
func (s *Server) agentGatewayAuth(ctx context.Context, token string) (gateway.Ref, error) {
	project, agent, err := s.store.AgentForToken(ctx, token)
	if err != nil {
		return gateway.Ref{}, err
	}
	return gateway.Ref{Project: project, Agent: agent}, nil
}

// newAgentGateway is the gateway every pod connects to, made once. Its log is
// the daemon's own, which is how a pod connecting and disconnecting is
// recorded; the gateway never logs the token, only the agent it named.
func (s *Server) newAgentGateway() *gateway.Server {
	g := gateway.NewServer(s.agentGatewayAuth)
	g.Log = s.logf
	return g
}

// gatewayState is the local listener: open while the daemon serves one, and the
// reason it couldn't be, when it couldn't.
type gatewayState struct {
	mu  sync.Mutex
	srv *http.Server // the listener, when it is open
	err string       // why it couldn't be
	// addr is where it listens, once it does: the address it really got, so a
	// test that asks for port zero can connect to it.
	addr string
}

// serveAgentGateway opens the listener a cluster on this machine dials. A
// cluster elsewhere reaches the daemon through the tailnet instead, so nothing
// is opened unless Config.GatewayAddr says where.
func (s *Server) serveAgentGateway(ctx context.Context) {
	addr := s.cfg.GatewayAddr
	if addr == "" || addr == "off" {
		return
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		g := &s.gatewayListener
		g.mu.Lock()
		g.err = fmt.Sprintf("%s can't be opened: %v", addr, err)
		reason := g.err
		g.mu.Unlock()
		s.logf("agent gateway off: %s", reason)
		return
	}
	srv := &http.Server{
		// The gateway alone, not routes(): a pod's token names one agent, and
		// nothing else this daemon serves is a pod's to reach.
		Handler:           s.gateway,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		// No write timeout: the connection is a WebSocket that lives as long
		// as the pod does, and a deadline would cut it.
	}
	g := &s.gatewayListener
	listening := ln.Addr().String()
	g.mu.Lock()
	g.addr, g.srv, g.err = listening, srv, ""
	g.mu.Unlock()
	go func() { _ = srv.Serve(ln) }()
	s.logf("serving the agent gateway on %s", listening)
}

// closeAgentGateway ends the listener as the daemon stops, and with it the
// sessions pods hold on it: the next daemon is a new gateway, and a pod
// reconnects to it on its own.
func (s *Server) closeAgentGateway() {
	g := &s.gatewayListener
	g.mu.Lock()
	if g.srv != nil {
		_ = g.srv.Close()
		g.srv = nil
	}
	g.mu.Unlock()
	_ = s.gateway.Close()
}
