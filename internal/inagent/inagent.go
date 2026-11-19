// Package inagent tells a process whether it runs on an agent's own machine.
// It is its own package so the daemon's update check (internal/update), which
// must never count an agent's throwaway daemon as an install, and the command
// line (internal/cli) decide it the same way.
package inagent

import (
	"os"
	"strings"

	"agentbox/internal/api"
)

// Seams for tests, which may themselves run on an agent: each names a path (or
// a hostname) to look at instead of the real one. Set to something that isn't
// there, they make a process look like it runs on a machine of its own.
const (
	MarkerEnv   = "AGENTBOX_IN_AGENT_MARKER"
	SocketEnv   = "AGENTBOX_IN_AGENT_SOCKET"
	HostnameEnv = "AGENTBOX_IN_AGENT_HOSTNAME"
)

// hostnamePrefix is agent.InstanceName's "ab-" prefix, kept here as a literal
// instead of importing internal/agent: Incus names every agent's container
// ab-<project>-<agent> and sets that as its hostname by default, so it's there
// even on an agent whose image predates the marker.
const hostnamePrefix = "ab-"

// Marker is api.InAgentMarker, unless AGENTBOX_IN_AGENT_MARKER says otherwise.
func Marker() string {
	if s := os.Getenv(MarkerEnv); s != "" {
		return s
	}
	return api.InAgentMarker
}

// Socket is api.InAgentSocket, unless AGENTBOX_IN_AGENT_SOCKET says otherwise.
func Socket() string {
	if s := os.Getenv(SocketEnv); s != "" {
		return s
	}
	return api.InAgentSocket
}

// hostname is os.Hostname, unless AGENTBOX_IN_AGENT_HOSTNAME says otherwise.
func hostname() (string, error) {
	if s := os.Getenv(HostnameEnv); s != "" {
		return s, nil
	}
	return os.Hostname()
}

// Inside reports whether this process runs on an agent's own machine. The
// marker provision.sh writes into every agent is the normal way to tell, but
// an agent made before the marker existed has none until its image is rebuilt,
// and its in-agent API socket can be missing for a while right after boot
// (internal/agent/agentapi.go's replugHiddenSocket): fall back to the socket,
// then to the container's own hostname, so an older or just-booted agent is
// never mistaken for a machine of its own.
func Inside() bool {
	if _, err := os.Stat(Marker()); err == nil {
		return true
	}
	if _, err := os.Stat(Socket()); err == nil {
		return true
	}
	host, err := hostname()
	return err == nil && strings.HasPrefix(host, hostnamePrefix)
}
