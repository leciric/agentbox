import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { ChevronDown, ChevronUp, Globe, List, LoaderCircle, Search, X } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from 'react';
import * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { findKinds, hitsByDay, localDay, stepHit, type FindFrom } from '../../lib/chatFind';
import { useLanguage, useT } from '../../lib/i18n';
import { openSearch } from '../../lib/searchOpen';
import { countFeature } from '../../lib/usageStats';
import { cn } from '../../lib/utils';
import { Menu, MenuContent, MenuItem, MenuTrigger } from '../ui/menu';
import { Tip } from '../ui/tooltip';
import { rangesIn, useRevealItem, type ChatOpenAt } from './reveal';

// FindBar is Ctrl+F (⌘F) in a chat. The daemon finds the query in the whole
// conversation, loaded or not (GET …/chat/search); the bar marks it in what
// is on the page, with the CSS Custom Highlight API so the timeline's own
// elements are left alone, and steps through the messages that hold it,
// reading the chat back to one older than any page loaded. It starts at the
// newest, as the chat does: Enter goes back through the conversation, Shift+
// Enter forward.
//
// It is docked across the top of the chat, and under it is the list of every
// message that holds the query, newest first and by day, to pick one from
// rather than step to it; From narrows the find to what you wrote or to what
// the AI tool said. The list folds away, and stays folded until it is asked
// for again.
export function FindBar({
  chatRef,
  thread,
  scroller,
  focus,
  start,
  onJump,
  onClose,
}: {
  chatRef: string;
  thread?: T.ChatThread;
  scroller: RefObject<HTMLDivElement | null>;
  focus: number; // changes each time the bar is asked for again, to take the keyboard back
  start?: ChatOpenAt; // what the bar opens on: a query, and the hit to start at
  onJump: () => void; // the chat stops following its end
  onClose: () => void;
}) {
  const t = useT();
  const input = useRef<HTMLInputElement>(null);
  const [text, setText] = useState(start?.query ?? '');
  const [query, setQuery] = useState(start?.query ?? '');
  useEffect(() => {
    const timer = setTimeout(() => setQuery(text), 150);
    return () => clearTimeout(timer);
  }, [text]);
  useLayoutEffect(() => {
    input.current?.focus();
    input.current?.select();
  }, [focus]);
  useEffect(() => countFeature(T.FeatureChatFind), []);
  const [from, setFrom] = useState<FindFrom>('anyone');
  const [listed, setListed] = useState(() => localStorage.getItem(listKey) !== '0');
  const showList = (on: boolean) => {
    setListed(on);
    localStorage.setItem(listKey, on ? '1' : '0');
  };

  // Messages that arrive while the bar is open are searched too.
  const count = thread?.items.length ?? 0;
  const found = useQuery({
    queryKey: ['chatSearch', chatRef, query, from, count],
    queryFn: () => api.searchChat(chatRef, query, findKinds(from)),
    enabled: query.trim() !== '',
    placeholderData: keepPreviousData,
  });
  const hits = (query.trim() !== '' && found.data?.hits) || noHits;

  // The hit you're on, by item. A new query starts again at the newest; the
  // same query searched again keeps it.
  const [current, setCurrent] = useState<string>();
  const { reveal, loading } = useRevealItem(chatRef, thread, scroller, onJump);
  const searched = useRef('');
  useEffect(() => {
    const data = found.data;
    if (!data || found.isPlaceholderData) return;
    const fresh = searched.current !== `${data.query}\n${from}`;
    searched.current = `${data.query}\n${from}`;
    // The first results of the query the bar opened with start at the item
    // it opened at.
    const next = stepHit(data.hits, fresh ? (data.query === start?.query ? start.item : undefined) : current, 'stay');
    setCurrent(next);
    if (fresh && next) reveal(next, data.query);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [found.data, found.isPlaceholderData, current]);
  const step = (direction: 'older' | 'newer') => {
    const next = stepHit(hits, current, direction);
    if (next) reveal(next, query);
    setCurrent(next);
  };

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
  const lead = chatRef.split('/')[1] === T.LeadName;
  const who = (kind: string) => (kind === 'user' || kind === 'aside' ? t('chat.find.from.you') : kind === 'assistant' ? (lead ? t('chat.find.from.lead') : t('chat.find.from.agent')) : t('chat.find.notice'));
  const froms: [FindFrom, string][] = [
    ['anyone', t('chat.find.from.anyone')],
    ['you', t('chat.find.from.you')],
    ['ai', lead ? t('chat.find.from.lead') : t('chat.find.from.agent')],
  ];
  const pick = (id: string) => {
    setCurrent(id);
    reveal(id, query);
  };
  return (
    <div
      className="z-30 shrink-0 border-b border-line bg-overlay shadow-sm backdrop-blur-xl"
      role="search"
      data-chat-find
      // Escape closes the bar from any of its controls, not only the input;
      // the From menu, in a portal outside it, closes itself first.
      onKeyDown={(event) => {
        if (event.key === 'Escape' && !event.defaultPrevented && event.currentTarget.contains(event.target as Node)) {
          event.preventDefault();
          onClose();
        }
      }}
    >
      <div className="mx-auto flex w-full max-w-4xl items-center gap-1 px-4 py-1.5 md:px-6">
        <Search className="size-4 shrink-0 text-subtle" />
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
          className="h-8 min-w-0 flex-1 bg-transparent text-[13.5px] text-primary placeholder:text-faint focus:outline-none"
          spellCheck={false}
        />
        <Menu>
          <MenuTrigger asChild>
            <button
              type="button"
              className={cn(
                'flex h-7 shrink-0 items-center gap-1 rounded-lg border px-2 text-[11.5px] transition-colors',
                from === 'anyone' ? 'border-line text-muted hover:text-title' : 'border-brand-400/40 bg-brand-400/15 text-brand-200',
              )}
              data-chat-find-from={from}
            >
              {t('chat.find.from', { who: froms.find(([f]) => f === from)![1] })}
              <ChevronDown className="size-3" />
            </button>
          </MenuTrigger>
          <MenuContent align="end">
            {froms.map(([f, label]) => (
              <MenuItem key={f} onSelect={() => setFrom(f)}>
                <span className={cn(f === from && 'text-brand-300')}>{label}</span>
              </MenuItem>
            ))}
          </MenuContent>
        </Menu>
        <span className="mx-1 h-4 w-px shrink-0 bg-line" />
        <span className="flex min-w-16 items-center justify-end gap-1 px-1 text-[12px] tabular-nums text-subtle" aria-live="polite" data-chat-find-count>
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
        <Tip label={listed ? t('chat.find.hideList') : t('chat.find.showList')}>
          <button className={cn(findButton, listed && 'bg-surface-raised text-title')} aria-pressed={listed} onClick={() => showList(!listed)} aria-label={t('chat.find.showList')} data-chat-find-list-toggle>
            <List className="size-4" />
          </button>
        </Tip>
        <Tip label={t('chat.find.everywhere')}>
          <button className={findButton} onClick={() => openSearch({ query: text, kind: T.SearchChats })} aria-label={t('chat.find.everywhere')} data-chat-find-everywhere>
            <Globe className="size-4" />
          </button>
        </Tip>
        <Tip label={t('chat.find.close')}>
          <button className={cn(findButton, 'ml-0.5')} onClick={onClose} aria-label={t('chat.find.close')}>
            <X className="size-4" />
          </button>
        </Tip>
      </div>
      {listed && hits.length > 0 && <HitList hits={hits} current={current} more={!!found.data?.more} who={who} onPick={pick} />}
    </div>
  );
}

// HitList is every message the find found, newest first, by day: who said
// it, the words around the match, and when, the one the bar is on lit.
function HitList({ hits, current, more, who, onPick }: { hits: T.ChatSearchHit[]; current?: string; more: boolean; who: (kind: string) => string; onPick: (id: string) => void }) {
  const t = useT();
  const language = useLanguage();
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    box.current?.querySelector('[data-chat-find-current]')?.scrollIntoView({ block: 'nearest' });
  }, [current]);
  const today = localDay(new Date());
  const yesterday = localDay(new Date(Date.now() - 24 * 60 * 60 * 1000));
  const dayLabel = (day: string) => {
    if (day === '') return t('chat.find.undated');
    if (day === today) return t('chat.find.today');
    if (day === yesterday) return t('chat.find.yesterday');
    const [y, m, d] = day.split('-').map(Number);
    const date = new Date(y, m - 1, d);
    return date.toLocaleDateString(language, { day: 'numeric', month: 'long', ...(y !== new Date().getFullYear() ? { year: 'numeric' } : {}) });
  };
  return (
    <div className="mx-auto w-full max-w-4xl px-4 pb-2 md:px-6">
      <div ref={box} className="max-h-[min(300px,38vh)] overflow-y-auto overscroll-contain rounded-xl border border-line bg-modal" data-chat-find-list>
        <div className="sticky top-0 z-10 border-b border-line bg-modal px-3 py-1.5 text-[11px] text-subtle">{t('chat.find.messages', { n: more ? `${hits.length}+` : String(hits.length) })}</div>
        {hitsByDay(hits).map((group) => (
          <div key={group.day}>
            <h3 className="px-3 pb-0.5 pt-2 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle">{dayLabel(group.day)}</h3>
            {group.hits.map((hit) => {
              const on = hit.id === current;
              const mine = hit.kind === 'user' || hit.kind === 'aside';
              return (
                <button
                  key={hit.id}
                  type="button"
                  className={cn('mx-1 flex w-[calc(100%-0.5rem)] items-start gap-3 rounded-lg px-2 py-1.5 text-left text-[12.5px] transition-colors', on ? 'bg-surface-raised' : 'hover:bg-surface-faint')}
                  onClick={() => onPick(hit.id)}
                  data-chat-find-hit={hit.id}
                  data-chat-find-current={on || undefined}
                >
                  <span className={cn('w-14 shrink-0 truncate text-[11.5px]', mine ? 'text-brand-300' : 'text-subtle')}>{who(hit.kind)}</span>
                  <span className="min-w-0 flex-1 truncate text-primary">
                    {hit.snippet.map((part, i) =>
                      part.match ? (
                        <mark key={i} className="rounded-[3px] bg-brand-400/25 text-inherit">
                          {part.text}
                        </mark>
                      ) : (
                        <span key={i}>{part.text}</span>
                      ),
                    )}
                  </span>
                  {hit.at && <span className="shrink-0 text-[11px] tabular-nums text-faint">{new Date(hit.at).toLocaleTimeString(language, { hour: '2-digit', minute: '2-digit' })}</span>}
                </button>
              );
            })}
          </div>
        ))}
      </div>
    </div>
  );
}

// Whether the list of hits shows, kept for the next find.
const listKey = 'agentbox.chat.findList';

const noHits: T.ChatSearchHit[] = [];

// The marks' colours. They are here rather than in styles.css because the CSS
// minifier doesn't know ::highlight() yet and warns on every build, though
// Chromium has it.
if (typeof document !== 'undefined') {
  const style = document.createElement('style');
  style.textContent = `
    ::highlight(chat-find) { background-color: color-mix(in srgb, #facc15 32%, transparent); }
    ::highlight(chat-find-current) { background-color: #f59e0b; color: #1c1917; }`;
  document.head.append(style);
}

const findButton = 'flex size-7 items-center justify-center rounded-lg text-subtle transition-colors hover:bg-surface-raised hover:text-title disabled:pointer-events-none disabled:opacity-40';
