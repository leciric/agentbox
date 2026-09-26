package agent

import (
	"context"
	"fmt"

	"agentbox/internal/image"
	"agentbox/internal/state"
)

// A project can turn nesting on: its agents get a real Incus daemon of their
// own, inside their own container, so agents working on AgentBox can test
// features that touch agent machines (limits, GPU, image builds) for real.
// It needs the base image built with Incus (image.Components.Incus), and it
// costs isolation: an agent with it can make and run containers of its own.
//
// The container's profile already sets security.nesting=true for every
// agent, which Docker already relies on; nothing more is needed there. What
// provision.sh leaves undone when Incus is built in is initializing it: the
// package installs with its daemon masked, off until a project asks for it,
// so an agent without nesting spends nothing on it.

// nestedBridgeSubnet is the nested Incus's own bridge, picked so it can never
// clash with the host's own incusbr0 (10.8.8.0/24 by default, or whatever
// --bridge-subnet chose): a private range host setup never offers.
const nestedBridgeSubnet = "10.88.8.1/24"

// nestingPreseed is `incus admin init`'s preseed, minimal on purpose: a dir
// storage pool, which needs no block device or filesystem support nested, and
// a bridge of its own rather than sharing the outer one. /dev/kvm isn't asked
// for: nesting is for containers, not VMs.
const nestingPreseed = `config: {}
networks:
- name: incusbr0
  type: bridge
  config:
    ipv4.address: ` + nestedBridgeSubnet + `
    ipv4.nat: "true"
    ipv6.address: none
storage_pools:
- name: default
  driver: dir
profiles:
- name: default
  devices:
    root:
      path: /
      pool: default
      type: disk
    eth0:
      name: eth0
      network: incusbr0
      type: nic
`

// nestingScript starts the agent's own Incus daemon and initializes it, unless
// another run already did: `incus admin init` refuses a daemon that already
// has a storage pool, which is what running it twice would otherwise hit.
const nestingScript = `set -eu
systemctl enable --now incus.socket incus
incus admin waitready --timeout 30
incus storage list -f csv 2>/dev/null | grep -q '^default,' && exit 0
incus admin init --preseed <<'PRESEED'
` + nestingPreseed + `PRESEED
`

// checkNesting reports that an agent of a project with nesting on could
// actually get it: the base image has Incus built in. Checked before a
// machine is copied, the way checkImageTool checks Codex and OpenCode.
func (m *Manager) checkNesting(ctx context.Context, p state.Project) error {
	if !p.Nesting {
		return nil
	}
	installed, err := image.InstalledBuild(ctx, m.Incus)
	if err != nil {
		return nil
	}
	if installed.Components.Incus {
		return nil
	}
	return fmt.Errorf("%s has nesting on, but the base image has no Incus: run agentbox image build --incus, or turn nesting off", p.Name)
}

// EnsureNesting starts and initializes the agent's own Incus daemon when its
// project has nesting on. Best-effort, like EnsureBrowser: a failure is logged
// rather than failing the agent's creation, since the daemon inside is a tool
// for testing with, not something the agent otherwise needs to run.
func (m *Manager) EnsureNesting(ctx context.Context, a state.Agent, p state.Project) {
	if !p.Nesting {
		return
	}
	m.logf("Setting up %s's own Incus", a.Ref())
	if err := m.setUpNesting(ctx, a); err != nil {
		m.logf("%s's nesting didn't start: %v", a.Ref(), err)
	}
}

func (m *Manager) setUpNesting(ctx context.Context, a state.Agent) error {
	if err := m.requireRunning(ctx, a); err != nil {
		return err
	}
	_, err := m.Incus.Run(ctx, "exec", a.Instance, "-T", "--", "bash", "-c", nestingScript)
	return err
}
