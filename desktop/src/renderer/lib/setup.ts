import type { ConnectionState, HostSetupStatus, LinuxSetup, VMStatus, WSLStatus } from '../../preload';

// SetupCard is what the app shows above the page while there's no daemon on a
// Mac, on Windows or on Linux: the VM or the WSL distro to set up, when that's
// why.
export type SetupCard = { kind: 'vm'; vm: VMStatus } | { kind: 'wsl'; wsl: WSLStatus } | { kind: 'linux'; linux: LinuxSetup } | null;

// setupCard picks it. It asks only whether the daemon is connected, not
// whether the app is between two attempts at it: until the distro exists every
// attempt fails, and a card that went away for each one flickered on Windows
// for as long as the first launch waited to be set up.
//
// On Linux it is AgentBox's Cloud Hypervisor VM, before `agentbox vm init`
// made it. A machine that runs AgentBox itself, set up before AgentBox ran in
// a VM on Linux, gets none: its daemon is its own, and the move into the VM
// is offered once it answers (MovePrompt).
//
// A Mac's VM being made again with krunkit (Settings' GPU for agents) is gone
// for a while too, but there's nothing to set up: the remake makes it.
export function setupCard(connection: ConnectionState, status: HostSetupStatus | undefined): SetupCard {
  if (connection.state === 'connected' || !status || status.remaking) return null;
  if (status.vm?.problem) return { kind: 'vm', vm: status.vm };
  if (status.wsl?.problem) return { kind: 'wsl', wsl: status.wsl };
  if (status.linux?.mode === 'vm' && status.chv?.state === 'missing') return { kind: 'linux', linux: status.linux };
  return null;
}
