// ?app=cockpit: cockpit mode (components/cockpit), turned on, over the
// Office's projects and agents (dev/office.ts), with the agents' desktops
// drawn (pipDesktops.tsx) and a few lines in each lead's conversation, so the
// feed has every project in it. &cockpit=1 with any ?app= only turns the
// setting on, and &cockpit=off leaves it off for a before/after.
import type { QueryClient } from '@tanstack/react-query';
import type * as T from '../../shared/api';
import { setDesktopStandIn } from '../components/pip/standIn';
import { chatKey } from '../lib/chat';
import { Desktop, type Scene } from './pipDesktops';

const min = 60_000;
const ago = (ms: number) => new Date(Date.now() - ms).toISOString();
const scenes: Scene[] = ['csv', 'dark', 'test', 'docs', 'ci', 'newproject'];

// Each other project's lead conversation: what you said and what it answered.
const conversations: Record<string, [kind: 'user' | 'assistant', text: string, minutesAgo: number][]> = {
  storefront: [
    ['user', 'Apple Pay at checkout this week, and the Safari empty-cart crash first.', 52],
    ['assistant', 'agent-3 is on the Safari crash: it reproduces with an empty cart and a saved coupon. agent-4 started Apple Pay and has a question for you about guests.', 47],
    ['assistant', 'agent-5 opened #212 for the product images; its CI fails on the image-size snapshot, which it is updating.', 9],
  ],
  quill: [
    ['user', 'Ship the Markdown export, then look at syncing drafts.', 130],
    ['assistant', 'Markdown export merged in #41. Draft sync is waiting for you to start agent-9 again: it stopped overnight.', 24],
  ],
};

export function seedCockpit(queryClient: QueryClient): void {
  const scene = new Map<string, Scene>();
  setDesktopStandIn((a: T.Agent) => {
    if (!scene.has(a.ref)) scene.set(a.ref, scenes[scene.size % scenes.length]);
    return <Desktop scene={scene.get(a.ref)!} />;
  });
  for (const [project, lines] of Object.entries(conversations)) {
    const items = lines.map(([kind, text, m], i) => ({ id: `${project}-lead-${i}`, turn: `${project}-${i}`, kind, text, createdAt: ago(m * min), updatedAt: ago(m * min) }) as T.ChatItem);
    const session = { state: 'off', tool: 'claude', options: [], commands: [] } as unknown as T.ChatSession;
    queryClient.setQueryData<T.ChatThread>(chatKey(`${project}/lead`), { agent: `${project}/lead`, seq: items.length, session, items });
  }
}
