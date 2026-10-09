// Bringing one thing forward on the page a link opened: the search palette
// opens a page, then the memory, the pull request or the chat message it found
// there is scrolled to and flashed. The request is made before the page
// exists, so the element is waited for; a panel that has to show something
// first (a sub-tab of Memory, an event older than its timeline) reads the
// request to know what.
import { useMemo, useSyncExternalStore } from 'react';
import type * as T from '../../shared/api';

export type MemorySection = 'memories' | 'events' | 'reports';

export type Reveal = {
  // selector finds the element to bring forward; a chat message (chat) has
  // its chat bring it forward instead, reading back to it if need be.
  selector?: string;
  // chat is a message of a project's chat (no agent), of an agent's, or of
  // the Home chat (project HomeProject), with the words searched for.
  chat?: { project: string; agent?: string; item: string; query?: string };
  // memory is the sub-tab of a project's Memory page it is on, and the
  // thing itself, for the page to show even when its list doesn't.
  memory?: { section: MemorySection; project: string; item: T.Memory | T.MemoryEvent | T.AgentReport };
  // pull is the pull request it is, for the list to show it whatever its state.
  pull?: { project: string; number: number };
  // line selects a line of a textarea (a project's notes), counted from 1.
  line?: number;
};

// How long a request waits for its page, and how long a page that mounts
// later still takes it as meant for it.
const waitMs = 6_000;

let current: (Reveal & { at: number }) | null = null;
const listeners = new Set<() => void>();

// requestReveal asks for what selector finds to be brought forward once it is
// on the page. Make it before going to the page.
export function requestReveal(reveal: Reveal): void {
  current = { ...reveal, at: Date.now() };
  for (const listener of listeners) listener();
  if (reveal.selector) void bringForward(reveal.selector, reveal);
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

// pendingReveal is the request still waiting for its page, if any.
export function pendingReveal(): Reveal | null {
  return current && Date.now() - current.at < waitMs ? current : null;
}

// useReveal is the latest request, for a panel that has to show something
// before the element can be found. It changes with each new request.
export function useReveal(): Reveal | null {
  return useSyncExternalStore(subscribe, () => current);
}

// useChatOpenAt is ChatTab's openAt for a message a search found in this
// chat (agent "" for a project's chat), while the request is new: a chat
// opened later, by hand, opens at its end as usual.
export function useChatOpenAt(project: string, agent = ''): { item: string; query?: string } | undefined {
  const reveal = useSyncExternalStore(subscribe, () => current);
  return useMemo(() => {
    const chat = reveal?.chat;
    if (!chat || chat.project !== project || (chat.agent ?? '') !== agent || Date.now() - reveal.at > waitMs) return undefined;
    return { item: chat.item, query: chat.query };
  }, [reveal, project, agent]);
}

async function bringForward(selector: string, reveal: Reveal) {
  const until = Date.now() + waitMs;
  let el: Element | null = null;
  while (!(el = document.querySelector(selector)) && Date.now() < until) {
    await new Promise((resolve) => setTimeout(resolve, 60));
  }
  if (!el) return;
  // One more frame, so a list still laying itself out doesn't move it away.
  await new Promise((resolve) => requestAnimationFrame(resolve));
  if (reveal.line !== undefined && el instanceof HTMLTextAreaElement) {
    selectLine(el, reveal.line);
    return;
  }
  el.scrollIntoView({ block: 'center', behavior: 'smooth' });
  el.setAttribute('data-revealed', '');
  setTimeout(() => el.removeAttribute('data-revealed'), 2_400);
}

// selectLine selects line n (from 1) of a textarea and scrolls it into view.
function selectLine(el: HTMLTextAreaElement, n: number) {
  const [start, end] = lineRange(el.value, n);
  el.scrollIntoView({ block: 'center', behavior: 'smooth' });
  el.focus({ preventScroll: true });
  el.setSelectionRange(start, end);
  const lineHeight = parseFloat(getComputedStyle(el).lineHeight) || 18;
  el.scrollTop = Math.max(0, (n - 3) * lineHeight);
}

// lineRange is where line n (from 1) of text starts and ends; past the last
// line, the end of the text.
export function lineRange(text: string, n: number): [number, number] {
  let start = 0;
  for (let i = 1; i < n; i++) {
    const next = text.indexOf('\n', start);
    if (next < 0) return [text.length, text.length];
    start = next + 1;
  }
  const end = text.indexOf('\n', start);
  return [start, end < 0 ? text.length : end];
}
