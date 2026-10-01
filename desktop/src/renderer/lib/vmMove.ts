import type { HostSetupStatus, VMMigration } from '../../preload';

// On Linux, AgentBox runs in a VM of its own (Cloud Hypervisor). A machine set
// up before that, to run agents on its own Incus (host mode), keeps working as
// it was until it moves: `agentbox vm migrate`, Settings → Setup → Move to a
// VM. After each update the app says so once, in a prompt, until it has moved.

// laterKey holds the app version whose prompt was put off with Later.
export const laterKey = 'agentbox.vm-move.later';

// movePrompt reports whether the prompt shows: on a host-mode installation
// with something to move, or a move half-way, and not put off for this
// version of the app. A version not known yet shows nothing, so the prompt
// doesn't come up before it can be put off for the right one.
export function movePrompt(status: HostSetupStatus | undefined, migration: VMMigration | null | undefined, version: string | undefined, later: string | null): boolean {
  if (!version || status?.linux?.mode !== 'host' || !migration) return false;
  if (migration.state !== 'available' && migration.state !== 'started') return false;
  return later !== version;
}
