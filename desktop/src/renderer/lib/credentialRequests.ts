// How the credential and connector requests in a project's chat are grouped:
// requests from several agents for the same thing are one row there, answered
// once for all of them, rather than the same form drawn once per agent.
import * as T from '../../shared/api.ts';
import { connectorRequest, RequestConnector } from './connectors.ts';

export function isOpenRequest(q: T.Question): boolean {
  return q.status === 'pending' || q.status === 'escalated';
}

// requestTarget is what a request asks for, the same string for two requests
// one answer settles: any GitHub account, a secret by its name, a connector
// by its name and server. One whose target can't be told is its own.
export function requestTarget(q: T.Question): string {
  if (q.kind === RequestConnector) {
    const req = connectorRequest(q);
    return req ? `connector:${req.name}@${req.url.replace(/\/+$/, '')}` : `id:${q.id}`;
  }
  if (q.kind === T.CredentialSecret) return q.secretName ? `secret:${q.secretName}` : `id:${q.id}`;
  return q.kind ?? `id:${q.id}`;
}

export interface RequestGroup {
  key: string;
  // Oldest first: the first is the one the group is drawn from.
  questions: T.Question[];
}

// groupRequests puts the requests that ask for the same thing and stand the
// same way together, in the order the first of each was asked. Waiting ones
// group whatever was said; settled ones only when they were settled alike,
// so an answered group stays one row and a refused one stays apart from it.
export function groupRequests(questions: readonly T.Question[]): RequestGroup[] {
  const groups = new Map<string, T.Question[]>();
  for (const q of questions.toSorted((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt))) {
    const key = `${requestTarget(q)}|${isOpenRequest(q) ? 'open' : `${q.status}|${q.answer ?? ''}`}`;
    groups.set(key, [...(groups.get(key) ?? []), q]);
  }
  return [...groups].map(([key, qs]) => ({ key, questions: qs }));
}
