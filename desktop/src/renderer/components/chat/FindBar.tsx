import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronDown, ChevronUp, LoaderCircle, Search, X } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from 'react';
import * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { loadThrough } from '../../lib/chat';
import { matchesIn, stepHit } from '../../lib/chatFind';
import { useT } from '../../lib/i18n';
import { countFeature } from '../../lib/usageStats';
import { cn } from '../../lib/utils';
import { Tip } from '../ui/tooltip';

// FindBar is Ctrl+F (⌘F) in a chat. The daemon finds the query in the whole
// conversation, loaded or not (GET …/chat/search); the bar marks it in what
// is on the page, with the CSS Custom Highlight API so the timeline's own
// elements are left alone, and steps through the messages that hold it,
// reading the chat back to one older than any page loaded. It starts at the
// newest, as the chat does: Enter goes back through the conversation, Shift+
// Enter forward.
export function FindBar({
  chatRef,
  thread,
  scroller,
  focus,
  onJump,
  onClose,
}: {
  chatRef: string;
  thread?: T.ChatThread;
  scroller: RefObject<HTMLDivElement | null>;
  focus: number; // changes each time the bar is asked for again, to take the keyboard back
  onJump: () => void; // the chat stops following its end
  onClose: () => void;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const [text, setText] = useState('');
  const [query, setQuery] = useState('');
  useEffect(() => {
    const timer = setTimeout(() => setQuery(text), 150);
    return () => clearTimeout(timer);
  }, [text]);
  useLayoutEffect(() => {
    input.current?.focus();
    input.current?.select();
  }, [focus]);
  useEffect(() => countFeature(T.FeatureChatFind), []);

  // Messages that arrive while the bar is open are searched too.
  const count = thread?.items.length ?? 0;
  const found = useQuery({
    queryKey: ['chatSearch', chatRef, query, count],
    queryFn: () => api.searchChat(chatRef, query),
    enabled: query.trim() !== '',
    placeholderData: keepPreviousData,
  });
  const hits = (query.trim() !== '' && found.data?.hits) || noHits;

  // The hit you're on, by item. A new query starts again at the newest; the
  // same query searched again keeps it.
  const [current, setCurrent] = useState<string>();
  // reveal is the hit to bring into view, once it's on the page.
  const reveal = useRef<string>(undefined);
  const searched = useRef('');
  useEffect(() => {
    const data = found.data;
    if (!data || found.isPlaceholderData) return;
    const fresh = searched.current !== data.query;
    searched.current = data.query;
    const next = stepHit(data.hits, fresh ? undefined : current, 'stay');
    setCurrent(next);
    if (fresh) reveal.current = next;
  }, [found.data, found.isPlaceholderData, current]);
  const step = (direction: 'older' | 'newer') => {
    const next = stepHit(hits, current, direction);
    reveal.current = next;
    setCurrent(next);
  };

  // Going to a hit: read the chat back to it if it isn't loaded, then put
  // it a third of the way down the view once it's on the page.
  const [loading, setLoading] = useState(false);
  useEffect(() => {
    const box = scroller.current;
    const id = reveal.current;
    if (!id || !box) return;
    const row = box.querySelector(`[data-chat-id="${CSS.escape(id)}"]`);
    if (!row) {
      if (loading || thread?.items.some((it) => it.id === id)) return;
      onJump();
      setLoading(true);
      // A hit the chat can't read back to (cleared meanwhile) is let go,
      // rather than read for again on every render.
      const forget = () => {
        if (reveal.current === id) reveal.current = undefined;
      };
      loadThrough(queryClient, chatRef, id)
        .then((ok) => ok || forget())
        .catch(forget)
        .finally(() => setLoading(false));
      return;
    }
    reveal.current = undefined;
    onJump();
    const first = rangesIn(row, query)[0];
    const rect = (first ?? row).getBoundingClientRect();
    const view = box.getBoundingClientRect();
    box.scrollTo({ top: box.scrollTop + rect.top - view.top - box.clientHeight / 3 });
    // A new search, or a step, or the page it waits for having rendered.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current, found.data, thread, loading]);

  // The marks follow the page: pages loaded, text streaming in, code
  // coloured after it renders.
  const latest = useRef({ hits, current, query });
  latest.current = { hits, current, query };
  useEffect(() => {
    const box = scroller.current;
    if (!box || typeof CSS === 'undefined' || !('highlights' in CSS)) return;
    let frame = 0;
    const mark = () => {
      frame = 0;
      const { hits, current, query } = latest.current;
      const all: Range[] = [];
      const here: Range[] = [];
      for (const hit of hits) {
        const row = box.querySelector(`[data-chat-id="${CSS.escape(hit.id)}"]`);
        if (!row) continue;
        const ranges = rangesIn(row, query);
        (hit.id === current ? here : all).push(...ranges);
      }
      const now = new Highlight(...here);
      now.priority = 1;
      CSS.highlights.set('chat-find', new Highlight(...all));
      CSS.highlights.set('chat-find-current', now);
    };
    const schedule = () => {
      if (!frame) frame = requestAnimationFrame(mark);
    };
    schedule();
    const observer = new MutationObserver(schedule);
    observer.observe(box, { subtree: true, childList: true, characterData: true });
    return () => {
      observer.disconnect();
      cancelAnimationFrame(frame);
    };
  }, [scroller, hits, current, query]);
  useEffect(
    () => () => {
      if (typeof CSS === 'undefined' || !('highlights' in CSS)) return;
      CSS.highlights.delete('chat-find');
      CSS.highlights.delete('chat-find-current');
    },
    [],
  );

  const at = current ? hits.findIndex((h) => h.id === current) : -1;
  const total = found.data?.more ? `${hits.length}+` : String(hits.length);
  const status =
    query.trim() === '' ? '' : found.isPending ? '' : hits.length === 0 ? t('chat.find.none') : t('chat.find.count', { current: at < 0 ? 0 : hits.length - at, total });
  return (
    <div
      className="absolute right-3 top-3 z-30 flex animate-fade-in items-center gap-1 rounded-xl border border-line-strong bg-overlay py-1 pl-2.5 pr-1 shadow-lg backdrop-blur-xl md:right-5"
      role="search"
      data-chat-find
    >
      <Search className="size-3.5 shrink-0 text-subtle" />
      <input
        ref={input}
        value={text}
        onChange={(event) => setText(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Escape') {
            event.preventDefault();
            onClose();
          } else if (event.key === 'Enter') {
            event.preventDefault();
            // Enter before the debounce has run searches what's typed now.
            if (text !== query) setQuery(text);
            else step(event.shiftKey ? 'newer' : 'older');
          }
        }}
        placeholder={t('chat.find.placeholder')}
        aria-label={t('chat.find.label')}
        className="h-7 w-44 min-w-0 bg-transparent text-[13px] text-primary placeholder:text-faint focus:outline-none sm:w-56"
        spellCheck={false}
      />
      <span className="flex min-w-14 items-center justify-end gap-1 px-1 text-[12px] tabular-nums text-subtle" aria-live="polite" data-chat-find-count>
        {(loading || (found.isFetching && hits.length === 0 && query.trim() !== '')) && <LoaderCircle className="size-3 animate-spin" />}
        {status}
      </span>
      <Tip label={t('chat.find.older')}>
        <button className={findButton} disabled={hits.length === 0} onClick={() => step('older')} aria-label={t('chat.find.older')} data-chat-find-older>
          <ChevronUp className="size-4" />
        </button>
      </Tip>
      <Tip label={t('chat.find.newer')}>
        <button className={findButton} disabled={hits.length === 0} onClick={() => step('newer')} aria-label={t('chat.find.newer')} data-chat-find-newer>
          <ChevronDown className="size-4" />
        </button>
      </Tip>
      <Tip label={t('chat.find.close')}>
        <button className={cn(findButton, 'ml-0.5')} onClick={onClose} aria-label={t('chat.find.close')}>
          <X className="size-4" />
        </button>
      </Tip>
    </div>
  );
}

const noHits: T.ChatSearchHit[] = [];

const findButton = 'flex size-7 items-center justify-center rounded-lg text-subtle transition-colors hover:bg-surface-raised hover:text-title disabled:pointer-events-none disabled:opacity-40';

// rangesIn is where query is in a row's text: its [data-chat-text] parts,
// each read whole so a match can run across the elements inside, like a
// bold word in a sentence.
function rangesIn(row: Element, query: string): Range[] {
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
