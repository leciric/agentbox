// The search palette's logic (components/SearchPalette.tsx): where each kind
// of thing found opens, the order the keyboard walks the results in, and
// which words of a result to mark as what matched.
import type { View } from '../App';
import * as T from '../../shared/api.ts';
import type { Reveal } from './reveal.ts';

export type SearchKind =
  | typeof T.SearchProjects
  | typeof T.SearchAgents
  | typeof T.SearchChats
  | typeof T.SearchMemories
  | typeof T.SearchEvents
  | typeof T.SearchReports
  | typeof T.SearchMedia
  | typeof T.SearchSkills
  | typeof T.SearchConnectors
  | typeof T.SearchNotes
  | typeof T.SearchPulls;

// A result, with the group it is in.
export type SearchItem = { kind: string; hit: T.SearchHit };

// searchItems is every result in the order they are shown, which is the
// order the arrow keys walk them in.
export function searchItems(results: T.SearchResults | undefined): SearchItem[] {
  return (results?.groups ?? []).flatMap((g) => g.hits.map((hit) => ({ kind: g.kind, hit })));
}

// moveActive is the result the arrow keys land on, going round at either end.
export function moveActive(index: number, by: number, count: number): number {
  if (count === 0) return 0;
  return (((index + by) % count) + count) % count;
}

// Where a result opens: a page of the app with something on it to bring
// forward, a section of Settings, or a media item in the viewer.
export type SearchTarget =
  | { open: 'view'; view: View; reveal?: Reveal }
  | { open: 'settings'; section: string; reveal?: Reveal }
  | { open: 'media'; item: T.MediaItem; project: string };

const css = (value: string) => value.replace(/["\\]/g, '\\$&');

// searchTarget is where a result opens; query is what was searched for, for
// the place to mark it (a chat's find bar).
export function searchTarget({ kind, hit }: SearchItem, query?: string): SearchTarget {
  const project = hit.project ?? '';
  const ref = hit.agent ? `${project}/${hit.agent}` : '';
  switch (kind) {
    case T.SearchAgents:
      return { open: 'view', view: { kind: 'agent', ref: hit.id } };
    case T.SearchChats: {
      const reveal = { chat: { project, agent: hit.agent, item: hit.id, query } };
      if (project === T.HomeProject) return { open: 'view', view: { kind: 'homeChat' }, reveal };
      return ref
        ? { open: 'view', view: { kind: 'agent', ref, tab: 'chat' }, reveal }
        : { open: 'view', view: { kind: 'project', project, tab: 'chat' }, reveal };
    }
    case T.SearchMemories:
    case T.SearchEvents:
    case T.SearchReports: {
      const [section, attr, item] =
        kind === T.SearchMemories
          ? (['memories', 'data-memory', hit.memory] as const)
          : kind === T.SearchEvents
            ? (['events', 'data-event', hit.event] as const)
            : (['reports', 'data-report', hit.report] as const);
      return {
        open: 'view',
        view: { kind: 'project', project, tab: 'memory' },
        reveal: { selector: `[${attr}="${css(hit.id)}"]`, memory: item && { section, project, item } },
      };
    }
    case T.SearchMedia:
      if (hit.media) return { open: 'media', item: hit.media, project };
      return { open: 'view', view: { kind: 'project', project, tab: 'media' } };
    case T.SearchSkills:
      return { open: 'settings', section: 'skills', reveal: { selector: `[data-skill="${css(hit.id)}"]` } };
    case T.SearchConnectors: {
      const reveal = { selector: `[data-connector="${css(hit.id)}"]` };
      if (!project) return { open: 'settings', section: 'connectors', reveal };
      return ref
        ? { open: 'view', view: { kind: 'agent', ref, tab: 'connectors' }, reveal }
        : { open: 'view', view: { kind: 'project', project, tab: 'connectors' }, reveal };
    }
    case T.SearchNotes:
      return {
        open: 'view',
        view: { kind: 'project', project, tab: 'brief' },
        reveal: { selector: '[data-project-notes]', line: Number(hit.id) },
      };
    case T.SearchPulls:
      return {
        open: 'view',
        view: { kind: 'project', project, tab: 'pulls' },
        reveal: { selector: `[data-pull-request="${css(hit.id)}"]`, pull: { project, number: Number(hit.id) } },
      };
    case T.SearchProjects:
    default:
      return { open: 'view', view: { kind: 'project', project: project || hit.id } };
  }
}

// highlight splits text into the parts that match a word of the query and
// the parts between, for the palette to mark the first.
export function highlight(text: string, query: string): { text: string; match: boolean }[] {
  const words = query
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .sort((a, b) => b.length - a.length);
  if (!text || words.length === 0) return text ? [{ text, match: false }] : [];
  const pattern = new RegExp(words.map((w) => w.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|'), 'gi');
  const out: { text: string; match: boolean }[] = [];
  let last = 0;
  for (const m of text.matchAll(pattern)) {
    if (m.index > last) out.push({ text: text.slice(last, m.index), match: false });
    out.push({ text: m[0], match: true });
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push({ text: text.slice(last), match: false });
  return out;
}
