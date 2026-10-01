import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';
import * as T from '../../shared/api.ts';

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}

export function humanBytes(n: number): string {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${i === 0 ? n : n.toFixed(1)} ${units[i]}`;
}

// bytesOf is a compact "used / size" for a meter, both in size's unit
// ("24 / 120 GiB"): whole numbers from 10 up, one decimal below. A used of 0
// or less is one not known yet, shown as "—" rather than "0 B".
export function bytesOf(used: number, size: number): string {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let i = 0;
  let scale = 1;
  while (size >= scale * 1024 && i < units.length - 1) {
    scale *= 1024;
    i++;
  }
  const num = (n: number) => {
    const v = n / scale;
    if (v >= 10 || i === 0) return v.toFixed(0);
    if (v > 0 && v < 0.1) return '<0.1';
    return String(Number(v.toFixed(1)));
  };
  return `${used > 0 ? num(used) : '—'} / ${num(size)} ${units[i]}`;
}

// parseBytes reads the sizes Incus takes, like the disk guard's floor.
export function parseBytes(size: string): number | undefined {
  const match = /^\s*([0-9.]+)\s*(B|kB|MB|GB|TB|KiB|MiB|GiB|TiB)?\s*$/.exec(size);
  if (!match) return undefined;
  const units: Record<string, number> = {
    B: 1,
    kB: 1e3,
    MB: 1e6,
    GB: 1e9,
    TB: 1e12,
    KiB: 1024,
    MiB: 1024 ** 2,
    GiB: 1024 ** 3,
    TiB: 1024 ** 4,
  };
  return Number(match[1]) * (units[match[2] ?? 'B'] ?? 1);
}

// humanRate is a rate in bytes a second, read the way humanBytes reads a size.
export function humanRate(n: number): string {
  return `${humanBytes(Math.round(n))}/s`;
}

// shortRate is a rate cut down to fit the rail and the agent list beside CPU
// and memory: "118M/s", "4.2M/s", "96K/s", "0B/s".
export function shortRate(n: number): string {
  const units = ['B', 'K', 'M', 'G'];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${i > 0 && n < 10 ? n.toFixed(1) : Math.round(n)}${units[i]}/s`;
}

// stallPressure is where a PSI "full" figure (the share of the last ten
// seconds nothing could run, waiting on disk or memory) counts as a stall:
// the user's desktop froze at io full ~35% and memory full ~19%. The daemon
// sets HostPressure.stalling at the same line (agent.StallPressure).
export const stallPressure = 10;

export function timeAgo(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 45) return 'just now';
  if (s < 3600) return `${Math.max(1, Math.round(s / 60))}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

// timeUntil is timeAgo's mirror, for a future date: when kept media expires.
export function timeUntil(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((new Date(iso).getTime() - now) / 1000));
  if (s < 3600) return `${Math.max(1, Math.round(s / 60))}m`;
  if (s < 86400) return `${Math.round(s / 3600)}h`;
  return `${Math.round(s / 86400)}d`;
}

export function duration(from: string, to?: string): string {
  const ms = (to ? new Date(to).getTime() : Date.now()) - new Date(from).getTime();
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
}

export function errorMessage(err: unknown): string {
  const message = err instanceof Error ? err.message : String(err);
  return message.replace(/^Error invoking remote method '[^']+': (Error: )?/, '');
}

export function shortCommit(sha: string): string {
  return sha.slice(0, 7);
}

// githubAccountLabel names a GitHub account by who it is on GitHub: two
// accounts are told apart by their logins, not by what they were called here.
// A login is missing only for an account stored before AgentBox remembered it.
export function githubAccountLabel(account: string, login?: string): string {
  if (!account) return '';
  return login ? `${login} (${account})` : account;
}

// githubErrorSentence says why a project's pull requests couldn't be read,
// naming the account they were read with — pull requests are read with the
// project's account, never with the machine's own gh, so a repository one
// account can't see is another account's everyday repository. What would fix
// it is left to whoever shows this, because the fixes are links.
export function githubErrorSentence(err: T.GitHubError): string {
  // The account leads here, unlike the header: the sentence is about the
  // choice that was made, and the login says which GitHub user that is.
  const account = err.login ? `${err.account} (${err.login})` : err.account;
  switch (err.kind) {
    case T.GitHubNoAccess:
      return `The account ${account} can't see ${err.repo}.`;
    case T.GitHubBadToken:
      return `GitHub refused the account ${account}: its token was revoked, or has expired.`;
    case T.GitHubNoAccount:
      return err.account
        ? `This project's GitHub account "${err.account}" isn't stored any more.`
        : `No GitHub account is stored, so ${err.repo || 'this repository'}'s pull requests can't be read.`;
    default:
      return `GitHub: ${err.message}`;
  }
}
