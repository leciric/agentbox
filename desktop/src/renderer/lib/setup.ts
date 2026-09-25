import type { ConnectionState, HostSetupStatus, VMStatus, WSLStatus } from '../../preload';

// SetupCard is what the app shows above the page while there's no daemon on a
// Mac or on Windows: the VM or the WSL distro to set up, when that's why.
export type SetupCard = { kind: 'vm'; vm: VMStatus } | { kind: 'wsl'; wsl: WSLStatus } | null;

// setupCard picks it. It asks only whether the daemon is connected, not
// whether the app is between two attempts at it: until the distro exists every
// attempt fails, and a card that went away for each one flickered on Windows
// for as long as the first launch waited to be set up.
export function setupCard(connection: ConnectionState, status: HostSetupStatus | undefined): SetupCard {
  if (connection.state === 'connected' || !status) return null;
  if (status.vm?.problem) return { kind: 'vm', vm: status.vm };
  if (status.wsl?.problem) return { kind: 'wsl', wsl: status.wsl };
  return null;
}
