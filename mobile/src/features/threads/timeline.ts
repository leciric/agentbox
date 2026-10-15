// A thread as the phone's timeline shows it: messages as bubbles, the work
// between them (tool calls, thoughts, subagents, answered permissions) folded
// into one row per run that names what it did last, and the cards that wait
// on you. A smaller cut of the desktop's timelineRows (renderer/lib/chat.ts).
import type * as T from '../../shared/api.ts';
import { isActive } from './thread.ts';

export type Row =
  | { kind: 'user' | 'assistant' | 'error' | 'note'; id: string; item: T.ChatItem }
  | { kind: 'work'; id: string; items: T.ChatItem[]; live: boolean }
  | { kind: 'permission' | 'agentPermission'; id: string; item: T.ChatItem };

const isWork = (it: T.ChatItem) => it.kind === 'tool' || it.kind === 'thought' || it.kind === 'subagent' || it.kind === 'plan' || (it.kind === 'permission' && !!it.permission?.outcome);

export function timelineRows(thread: T.ChatThread): Row[] {
  const rows: Row[] = [];
  for (const it of thread.items) {
    if (it.parent || it.hidden) continue;
    // One of the project's agents asking permission, in its lead's chat.
    if (it.agentPermission) {
      rows.push({ kind: 'agentPermission', id: it.id, item: it });
      continue;
    }
    if (isWork(it)) {
      const last = rows.at(-1);
      if (last?.kind === 'work') {
        last.items.push(it);
        last.live ||= isActive(it);
      } else rows.push({ kind: 'work', id: it.id, items: [it], live: isActive(it) });
      continue;
    }
    switch (it.kind) {
      case 'user':
      case 'aside':
        if (it.text?.trim() || it.images?.length) rows.push({ kind: 'user', id: it.id, item: it });
        break;
      case 'assistant':
        if (it.text?.trim()) rows.push({ kind: 'assistant', id: it.id, item: it });
        break;
      case 'permission':
        rows.push({ kind: 'permission', id: it.id, item: it });
        break;
      case 'error':
        rows.push({ kind: 'error', id: it.id, item: it });
        break;
      default:
        // A handoff, a message from another project's chat, a compaction:
        // a line of its text, when it has one.
        if (it.text?.trim()) rows.push({ kind: 'note', id: it.id, item: it });
    }
  }
  return rows;
}
