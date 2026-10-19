// Picture-in-picture desktops in the lead's chat (UI v2): which agents get a
// tile in the dock, and the
// remark plugin that finds the mentions. components/pip draws them.
import type * as T from '../../shared/api';

// inUse is how long after an agent last used its desktop its tile stays: long
// enough to bridge the pauses between an agent's clicks while it thinks, short
// enough that a desktop it's done with goes.
export const inUse = 2 * 60_000;

// docked is the project's running agents that are using their desktops, in
// the order they were made, so a tile doesn't jump about as each one acts.
export function docked(project: string, agents: T.Agent[], uses: T.DesktopUse[], now: number): T.Agent[] {
  const used = new Map(uses.map((u) => [u.ref, new Date(u.usedAt).getTime()]));
  return agents
    .filter((a) => a.project === project && a.state === 'running' && now - (used.get(a.ref) ?? -Infinity) < inUse)
    .sort((a, b) => a.createdAt.localeCompare(b.createdAt));
}

// mentionHref is the link a mention becomes in a message's markdown: a
// fragment, so react-markdown's URL filter keeps it.
export const mentionPrefix = '#agentbox-agent=';
export const mentionHref = (name: string) => `${mentionPrefix}${encodeURIComponent(name)}`;
export const mentionOf = (href: string | undefined) => (href?.startsWith(mentionPrefix) ? decodeURIComponent(href.slice(mentionPrefix.length)) : undefined);

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
