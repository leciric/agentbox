// Paths between Windows and AgentBox's WSL distro, the way agentbox.exe
// translates them (LinuxPath and WindowsPath in internal/hostwsl): the daemon
// only knows Linux paths, and Windows's Explorer and folder picker only
// Windows ones.
export const distro = process.env.AGENTBOX_WSL_DISTRO || 'AgentBox';

const drivePath = /^([A-Za-z]):(?:[\\/](.*))?$/;
const wslPath = /^[\\/]{2}(?:wsl\.localhost|wsl\$)[\\/]([^\\/]+)(?:[\\/](.*))?$/i;

// linuxPath is where a Windows path is in the distro: C:\Users\ana is
// /mnt/c/Users/ana, and \\wsl.localhost\AgentBox\home\ana is /home/ana. null
// for a path the distro can't see: another distro's, or a network share's.
export function linuxPath(p: string, name = distro): string | null {
  if (p.startsWith('/')) return p;
  let m = drivePath.exec(p);
  if (m) {
    const rest = (m[2] ?? '').replace(/\\/g, '/').replace(/\/+$/, '');
    return `/mnt/${m[1].toLowerCase()}${rest ? `/${rest}` : ''}`;
  }
  m = wslPath.exec(p);
  if (m && m[1].toLowerCase() === name.toLowerCase()) return `/${(m[2] ?? '').replace(/\\/g, '/').replace(/\/+$/, '')}`;
  return null;
}

// windowsPath is where Windows sees a path of the distro.
export function windowsPath(p: string, name = distro): string {
  const m = /^\/mnt\/([a-z])(\/.*)?$/.exec(p);
  if (m) return `${m[1].toUpperCase()}:${(m[2] ?? '/').replace(/\//g, '\\')}`;
  return `\\\\wsl.localhost\\${name}${p.replace(/\//g, '\\')}`;
}
