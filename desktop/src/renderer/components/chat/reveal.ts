import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useRef, useState, type RefObject } from 'react';
import type * as T from '../../../shared/api';
import { chatWindowKey, dropWindow, holds, openWindow } from '../../lib/chat';
import { matchesIn } from '../../lib/chatFind';

// ChatOpenAt opens a chat at one of its items rather than at its end: what
// a search found, in this chat's find bar or across chats. With a query, the
// chat's find bar opens on it, at that item, with every match marked.
export interface ChatOpenAt {
  item: string; // the item's id, as a search hit has it
  query?: string;
}

// useChatWindow is the window the chat shows instead of its end, if any
// (openWindow).
export function useChatWindow(chatRef: string): T.ChatThread | null {
  return useQuery<T.ChatThread | null>({ queryKey: chatWindowKey(chatRef), queryFn: () => null, enabled: false, staleTime: Infinity }).data ?? null;
}

// useRevealItem's reveal brings an item of a chat into view: one the chat
// doesn't hold, it opens a window on (openWindow), and one the chat holds
// while a window is open, it goes back to the chat for. Once its row is on
// the page, it scrolls it a third of the way down the view — to the first
// match of query in it, given one. onJump is called as it does, for the chat
// to stop following its end. An item the daemon no longer has (cleared
// meanwhile) is let go. loading is while it reads.
export function useRevealItem(
  chatRef: string,
  thread: T.ChatThread | undefined,
  scroller: RefObject<HTMLDivElement | null>,
  onJump: () => void,
): { reveal: (item: string, query?: string) => void; loading: boolean } {
  const queryClient = useQueryClient();
  const win = useChatWindow(chatRef);
  const target = useRef<{ item: string; query?: string }>(undefined);
  const [asked, setAsked] = useState(0);
  const [loading, setLoading] = useState(false);
  const jump = useRef(onJump);
  jump.current = onJump;
  useEffect(() => {
    const box = scroller.current;
    const want = target.current;
    if (!want || !box) return;
    const row = box.querySelector(`[data-chat-id="${CSS.escape(want.item)}"]`);
    if (!row) {
      if (loading || holds(win ?? thread, want.item)) return;
      jump.current();
      if (win && holds(thread, want.item)) {
        dropWindow(queryClient, chatRef);
        return;
      }
      setLoading(true);
      const forget = () => {
        if (target.current === want) target.current = undefined;
      };
      openWindow(queryClient, chatRef, want.item)
        .then((ok) => ok || forget())
        .catch(forget)
        .finally(() => setLoading(false));
      return;
    }
    target.current = undefined;
    jump.current();
    const first = want.query ? rangesIn(row, want.query)[0] : undefined;
    const rect = (first ?? row).getBoundingClientRect();
    const view = box.getBoundingClientRect();
    box.scrollTo({ top: box.scrollTop + rect.top - view.top - box.clientHeight / 3 });
  }, [asked, thread, win, loading, chatRef, queryClient, scroller]);
  const reveal = useCallback((item: string, query?: string) => {
    target.current = { item, query };
    setAsked((n) => n + 1);
  }, []);
  return { reveal, loading };
}

// rangesIn is where query is in a row's text: its [data-chat-text] parts,
// each read whole so a match can run across the elements inside, like a
// bold word in a sentence.
export function rangesIn(row: Element, query: string): Range[] {
  const out: Range[] = [];
  const parts = row.matches('[data-chat-text]') ? [row] : Array.from(row.querySelectorAll('[data-chat-text]'));
  for (const part of parts) {
    const nodes: Text[] = [];
    const starts: number[] = [];
    let text = '';
    const walker = document.createTreeWalker(part, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      nodes.push(node as Text);
      starts.push(text.length);
      text += node.nodeValue ?? '';
    }
    // The text node holding index i, and i's offset in it.
    const locate = (i: number, end: boolean) => {
      let k = starts.length - 1;
      while (k > 0 && (end ? starts[k] >= i : starts[k] > i)) k--;
      return [nodes[k], i - starts[k]] as const;
    };
    for (const [s, e] of matchesIn(text, query)) {
      const range = document.createRange();
      range.setStart(...locate(s, false));
      range.setEnd(...locate(e, true));
      out.push(range);
    }
  }
  return out;
}
