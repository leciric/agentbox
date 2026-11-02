package agent

import (
	"context"
	_ "embed"
	"strings"

	"agentbox/internal/state"
)

// An agent's Docker starts on first use and stops when idle, rather than
// running from boot: dockerd and containerd hold a hundred MiB or more in
// every machine, most of which never run a container. The base image has
// docker.socket, which starts docker.service on the first connection to
// /run/docker.sock, so taking docker.service and containerd.service out of
// the boot is all it takes, and docker-idle.sh, every minute, stops both
// again once nothing has used them for a few minutes. It is set up as each
// machine starts, with no base image rebuild.
//
// A machine with containers Docker restarts by itself (--restart always or
// unless-stopped) still starts Docker as it boots, so they come back up as
// they did; Docker stops again once they have stopped.

//go:embed docker-idle.sh
var dockerIdleScript string

// dockerOnDemand sets it up, as root. It does nothing to a machine whose
// Docker has no socket unit to start it.
const dockerOnDemand = `set -e
[ -e /usr/lib/systemd/system/docker.socket ] || [ -e /lib/systemd/system/docker.socket ] || [ -e /etc/systemd/system/docker.socket ] || exit 0
cat >/usr/local/bin/agentbox-docker-idle <<'AGENTBOX_EOF'
@SCRIPT@AGENTBOX_EOF
chmod 755 /usr/local/bin/agentbox-docker-idle
cat >/etc/systemd/system/agentbox-docker-idle.service <<'AGENTBOX_EOF'
[Unit]
Description=Stop Docker while nothing uses it (AgentBox)
After=docker.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/agentbox-docker-idle
AGENTBOX_EOF
cat >/etc/systemd/system/agentbox-docker-idle.timer <<'AGENTBOX_EOF'
[Unit]
Description=Stop Docker while nothing uses it (AgentBox)

[Timer]
OnBootSec=1min
OnUnitActiveSec=1min
AccuracySec=10s

[Install]
WantedBy=timers.target
AGENTBOX_EOF
systemctl daemon-reload
systemctl disable -q docker.service containerd.service 2>/dev/null || true
systemctl enable -q --now docker.socket agentbox-docker-idle.timer
if grep -qsE '"RestartPolicy":\{"Name":"(always|unless-stopped)"' /var/lib/docker/containers/*/hostconfig.json; then
  systemctl start --no-block docker.service
fi
`

// EnsureDockerOnDemand sets a's Docker to start on first use and stop when
// idle. Best-effort, like EnsureBrowser: Docker works either way.
func (m *Manager) EnsureDockerOnDemand(ctx context.Context, a state.Agent) {
	script := strings.Replace(dockerOnDemand, "@SCRIPT@", dockerIdleScript, 1)
	if _, err := m.Incus.Exec(ctx, a.Instance, "bash", "-c", script); err != nil {
		m.logf("%s's Docker runs from boot, as before: %v", a.Ref(), err)
	}
}
