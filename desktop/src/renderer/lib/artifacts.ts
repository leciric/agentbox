// A project's Hatch artifacts: the pages its chats published on Hatch, read
// by the daemon out of those chats (internal/hatch). One query per project
// holds them all; the lead's chat shows every one, an agent's chat those it
// made or updated.
import type { QueryClient } from '@tanstack/react-query';
import * as T from '../../shared/api.ts';

export const artifactsKey = (project: string) => ['artifacts', project] as const;

// isLeadRef is a chat ref that is a project's chat, its lead's.
const isLeadRef = (ref: string) => {
  const [, name] = ref.split('/');
  return !name || name === T.LeadName;
};

// artifactsFor is what the chat of ref shows: nothing without a Hatch
// connector, all of them in the project's chat, an agent's own in its.
export function artifactsFor(list: T.Artifacts | undefined, ref: string): T.Artifact[] {
  if (!list?.connector) return [];
  if (isLeadRef(ref)) return list.artifacts;
  return list.artifacts.filter((a) => a.agents.includes(ref));
}

// The two Hatch calls that make or change an artifact, however the AI tool
// names them (internal/hatch.Op).
const pageTool = /(?:^|[^a-z0-9])(publish_page|update_page)$/;
export const isPageCall = (tool: Pick<T.ChatTool, 'name' | 'title'> | undefined): boolean =>
  !!tool && [tool.name, tool.title].some((s) => !!s && pageTool.test(s.trim().toLowerCase()));

// onChatItem refetches a project's artifacts when one of its chats finished
// a call to publish_page or update_page.
export function onChatItem(queryClient: QueryClient, chatRef: string, item: T.ChatItem): void {
  if (item.kind !== 'tool' || item.tool?.status !== 'completed' || !isPageCall(item.tool)) return;
  void queryClient.invalidateQueries({ queryKey: artifactsKey(chatRef.split('/')[0]) });
}

// Expiry is where an artifact stands: gone, going within the hour, there for
// now, kept for good, or not known (an update whose publish no chat has).
export type Expiry = 'expired' | 'soon' | 'later' | 'permanent' | 'unknown';

export function expiryOf(a: Pick<T.Artifact, 'expiresAt' | 'permanent' | 'expired'>, now = Date.now()): Expiry {
  if (a.expired) return 'expired';
  if (a.permanent) return 'permanent';
  if (!a.expiresAt) return 'unknown';
  const left = new Date(a.expiresAt).getTime() - now;
  if (left <= 0) return 'expired';
  return left < 3_600_000 ? 'soon' : 'later';
}

// byState puts the artifacts still there first, each group newest first as
// the daemon sends them.
export function byState(list: T.Artifact[], now = Date.now()): T.Artifact[] {
  return [...list].sort((a, b) => Number(expiryOf(a, now) === 'expired') - Number(expiryOf(b, now) === 'expired'));
}

// artifactPagePath is the daemon path of an artifact's HTML, which the app
// frames through agentbox-media://api (main/media.ts).
export const artifactPagePath = (project: string, id: string) =>
  `/v1/projects/${encodeURIComponent(project)}/artifacts/${encodeURIComponent(id)}/page`;
