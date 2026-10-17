// Picture-in-picture desktops in the lead's chat (UI v2): which agents get a
// tile in the dock, which capture a mention of an agent opens on, and the
// remark plugin that finds the mentions. components/pip draws them.
import type * as T from '../../shared/api';

// justFinished is how long an agent that finished keeps its tile, marked
// ready to review, before it leaves the dock.
export const justFinished = 15 * 60_000;

// docked is the project's agents that get a tile: those at work, in the
// order they were made, then those that finished in the last justFinished
// and wait for review, each with when it finished.
export function docked(project: string, agents: T.Agent[], review: T.ReviewItem[], now: number): { agent: T.Agent; finishedAt?: string }[] {
  const mine = agents.filter((a) => a.project === project && a.state === 'running').sort((a, b) => a.createdAt.localeCompare(b.createdAt));
  const ready = new Map(review.filter((i) => i.project === project).map((i) => [i.ref, i.finishedAt]));
  const out: { agent: T.Agent; finishedAt?: string }[] = [];
  for (const agent of mine) {
    const finishedAt = ready.get(agent.ref);
    if (finishedAt) {
      if (now - new Date(finishedAt).getTime() < justFinished) out.push({ agent, finishedAt });
    } else if (agent.chat === 'running' || agent.chat === 'waiting' || agent.chat === 'starting') out.push({ agent });
  }
  // The ones that finished come first: they are what to look at.
  return [...out.filter((d) => d.finishedAt), ...out.filter((d) => !d.finishedAt)];
}

export const captureKinds = ['recording', 'screenshot'];

// reportWindow is how long before a message a capture can be what it
// reports: the agent's work since the lead last heard from it.
export const reportWindow = 45 * 60_000;

// reported is the capture a message written at `at` reports, from an
// agent's (newest first): its latest recording from the reportWindow before
// it (a minute after, for one saved as the message streamed), else its
// latest screenshot then, else none.
export function reported(items: T.MediaItem[], at: string): T.MediaItem | undefined {
  const t = new Date(at).getTime();
  const near = items.filter((i) => {
    const c = new Date(i.createdAt).getTime();
    return captureKinds.includes(i.kind) && c <= t + 60_000 && c >= t - reportWindow;
  });
  return near.find((i) => i.kind === 'recording') ?? near[0];
}

// latestBy is an agent's latest capture as of `at`, or its latest at all: what
// a mention opens on when it was clicked rather than reported.
export function latestBy(items: T.MediaItem[], at: string): T.MediaItem | undefined {
  const t = new Date(at).getTime() + 60_000;
  const all = items.filter((i) => captureKinds.includes(i.kind));
  return all.find((i) => new Date(i.createdAt).getTime() <= t) ?? all[0];
}

// mentionHref is the link a mention becomes in a message's markdown: a
// fragment, so react-markdown's URL filter keeps it.
export const mentionPrefix = '#agentbox-agent=';
export const mentionHref = (name: string) => `${mentionPrefix}${encodeURIComponent(name)}`;
export const mentionOf = (href: string | undefined) => (href?.startsWith(mentionPrefix) ? decodeURIComponent(href.slice(mentionPrefix.length)) : undefined);

// mentionsIn is the agents a text names, in order, each once.
export function mentionsIn(text: string, names: ReadonlySet<string>): string[] {
  const re = mentionPattern(names);
  if (!re) return [];
  return [...new Set(Array.from(text.matchAll(re), (m) => m[2]))];
}

const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

// mentionPattern matches a name as a word of its own: agent-12 but not
// agent-123, nor agentbox/agent-12-csv. Its second group is the name.
function mentionPattern(names: ReadonlySet<string>): RegExp | null {
  if (names.size === 0) return null;
  const alt = [...names].sort((a, b) => b.length - a.length).map(escape).join('|');
  return new RegExp(`(^|[^\\w/-])(${alt})(?![\\w/-]|\\.\\w)`, 'g');
}

type MdNode = { type: string; value?: string; url?: string; children?: MdNode[] };

// remarkAgentMentions makes each of names in a message's text a link to
// mentionHref, leaving code and links as they are.
export function remarkAgentMentions(names: ReadonlySet<string>) {
  const re = mentionPattern(names);
  const walk = (node: MdNode) => {
    if (!node.children || node.type === 'link' || node.type === 'linkReference') return;
    const out: MdNode[] = [];
    for (const child of node.children) {
      if (child.type !== 'text' || !re) {
        walk(child);
        out.push(child);
        continue;
      }
      const text = child.value ?? '';
      let last = 0;
      for (const m of text.matchAll(re)) {
        const start = m.index + m[1].length;
        if (start > last) out.push({ type: 'text', value: text.slice(last, start) });
        out.push({ type: 'link', url: mentionHref(m[2]), children: [{ type: 'text', value: m[2] }] });
        last = start + m[2].length;
      }
      if (last < text.length) out.push(last === 0 ? child : { type: 'text', value: text.slice(last) });
    }
    node.children = out;
  };
  return () => (tree: MdNode) => walk(tree);
}

export type Spot = { x: number; y: number };

// clampSpot keeps a floating tile of size w×h inside an area of size
// width×height, a margin from its edges.
export function clampSpot(at: Spot, w: number, h: number, width: number, height: number, margin = 8): Spot {
  return {
    x: Math.round(Math.max(margin, Math.min(width - w - margin, at.x))),
    y: Math.round(Math.max(margin, Math.min(height - h - margin, at.y))),
  };
}
