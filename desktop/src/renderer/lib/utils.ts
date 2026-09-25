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
