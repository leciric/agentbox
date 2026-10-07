package agent

import (
	"context"
	"fmt"
	"strings"

	"agentbox/internal/state"
)

// TmpSize caps the tmpfs on an agent's /t (TMPDIR). Without a size= the
// kernel lets a tmpfs grow to half the memory it sees, which in AgentBox's VM
// is the whole VM's: an agent that cloned a repository there with its
// node_modules held it all in memory that can't be reclaimed without swap,
// and pushed the VM into thrashing. 2 GiB is plenty for temporary files, test
// directories and sockets; anything bigger belongs in the home directory, on
// disk. A full /t fails the write with ENOSPC rather than taking memory from
// every other agent.
const TmpSize = "2G"

// tmpScript caps /t at TmpSize: in fstab, for every later boot (a reboot from
// inside the agent included), and on the mount itself, for this one. The base
// image (system.sh) writes the fstab line without a size; doing it here
// instead needs no rebuild, and reaches agents made before it. A base with no
// /t line has no tmpfs there to cap. Remounting smaller than what /t already
// holds fails: that agent stays as it is until it starts again.
func tmpScript(fstab string) string {
	line := "tmpfs /t tmpfs mode=1777,nosuid,nodev,size=" + TmpSize + " 0 0"
	return fmt.Sprintf(`set -e
grep -q '^tmpfs /t ' %[1]s || exit 0
grep -qx '%[2]s' %[1]s || sed -i 's|^tmpfs /t .*|%[2]s|' %[1]s
if mountpoint -q /t; then mount -o remount,size=%[3]s /t; fi
`, fstab, line, TmpSize)
}

// CapTmp caps a running agent's /t (tmpScript). Like the package caches, it
// never keeps an agent from starting: one it can't cap is logged.
func (m *Manager) CapTmp(ctx context.Context, a state.Agent) {
	if out, err := m.Incus.Exec(ctx, a.Instance, "sh", "-c", tmpScript("/etc/fstab")); err != nil {
		m.logf("couldn't cap %s's /t at %s: %v: %s", a.Ref(), TmpSize, err, strings.TrimSpace(out))
	}
}
