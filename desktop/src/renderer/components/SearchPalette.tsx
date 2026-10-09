import * as DialogPrimitive from '@radix-ui/react-dialog';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import {
  Bot,
  Brain,
  CornerDownLeft,
  FileText,
  FolderGit2,
  GitPullRequest,
  History,
  Image,
  LoaderCircle,
  MessageSquare,
  NotebookPen,
  Plug,
  Search as SearchIcon,
  Wand2,
  type LucideIcon,
} from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT, type MessageKey } from '../lib/i18n';
import { projectLabel } from '../lib/projectName';
import { highlight, moveActive, searchItems, searchTarget, type SearchItem, type SearchTarget } from '../lib/search';
import { countFeature } from '../lib/usageStats';
import { cn, errorMessage, timeAgo } from '../lib/utils';

const kinds: Record<string, { label: MessageKey; icon: LucideIcon }> = {
  [T.SearchProjects]: { label: 'search.group.projects', icon: FolderGit2 },
  [T.SearchAgents]: { label: 'search.group.agents', icon: Bot },
  [T.SearchChats]: { label: 'search.group.chats', icon: MessageSquare },
  [T.SearchMemories]: { label: 'search.group.memories', icon: Brain },
  [T.SearchEvents]: { label: 'search.group.events', icon: History },
  [T.SearchReports]: { label: 'search.group.reports', icon: FileText },
  [T.SearchMedia]: { label: 'search.group.media', icon: Image },
  [T.SearchSkills]: { label: 'search.group.skills', icon: Wand2 },
  [T.SearchConnectors]: { label: 'search.group.connectors', icon: Plug },
  [T.SearchNotes]: { label: 'search.group.notes', icon: NotebookPen },
  [T.SearchPulls]: { label: 'search.group.pulls', icon: GitPullRequest },
};

// How long the typing has to pause before the daemon is asked.
const debounceMs = 140;

// isSearchShortcut is Ctrl+K, or ⌘K on a Mac, which opens the palette from
// anywhere but a terminal, where Ctrl+K is the shell's.
export function isSearchShortcut(event: KeyboardEvent): boolean {
  if (event.key.toLowerCase() !== 'k' || event.altKey || event.shiftKey || !(event.ctrlKey || event.metaKey)) return false;
  return !(event.target instanceof Element && event.target.closest('.xterm'));
}

// SearchPalette searches everything AgentBox keeps, in every project, while
// you type: one call to the daemon per pause (GET /v1/search), the results in
// groups by kind, walked with the arrow keys and opened with Enter, each where
// it lives in the app.
export function SearchPalette({ open, onOpenChange, onOpen }: { open: boolean; onOpenChange: (open: boolean) => void; onOpen: (target: SearchTarget) => void }) {
  const t = useT();
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 animate-fade-in bg-scrim backdrop-blur-[3px]" />
        <DialogPrimitive.Content
          aria-describedby={undefined}
          className="fixed left-1/2 top-[12vh] z-50 flex max-h-[76vh] w-[calc(100vw-2rem)] max-w-2xl -translate-x-1/2 animate-pop-in flex-col overflow-hidden rounded-2xl border border-line-strong bg-modal shadow-[0_40px_120px_-30px_var(--ab-shadow-deep)] focus:outline-none"
          data-search-palette
        >
          <DialogPrimitive.Title className="sr-only">{t('search.title')}</DialogPrimitive.Title>
          {open && (
            <Palette
              onOpen={(target) => {
                onOpenChange(false);
                onOpen(target);
              }}
            />
          )}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

function Palette({ onOpen }: { onOpen: (target: SearchTarget) => void }) {
  const t = useT();
  const [query, setQuery] = useState('');
  const [asked, setAsked] = useState('');
  const [active, setActive] = useState(0);
  const list = useRef<HTMLDivElement>(null);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });

  useEffect(() => countFeature(T.FeatureSearchOpen), []);
  useEffect(() => {
    const timer = setTimeout(() => setAsked(query.trim()), debounceMs);
    return () => clearTimeout(timer);
  }, [query]);

  const results = useQuery({
    queryKey: ['search', asked],
    queryFn: () => api.search(asked),
    enabled: asked !== '',
    // The last answer stays up while the next is on its way, so the list
    // doesn't blink empty at every keystroke.
    placeholderData: keepPreviousData,
    staleTime: 5_000,
  });
  const shown = asked === '' ? undefined : results.data;
  const items = searchItems(shown);
  const pending = query.trim() !== asked || results.isFetching;

  // A new answer starts at its first result.
  useEffect(() => setActive(0), [shown]);
  useEffect(() => {
    list.current?.querySelector('[data-search-active]')?.scrollIntoView({ block: 'nearest' });
  }, [active]);

  const agentTitle = (project: string, name: string) => agents.data?.find((a) => a.ref === `${project}/${name}`)?.title || name;
  const open = (item: SearchItem | undefined) => item && onOpen(searchTarget(item));

  let index = -1;
  return (
    <>
      <div className="flex items-center gap-3 border-b border-line px-4">
        {pending && asked !== '' ? <LoaderCircle className="size-4 shrink-0 animate-spin text-subtle" /> : <SearchIcon className="size-4 shrink-0 text-subtle" />}
        <input
          autoFocus
          aria-label={t('search.label')}
          className="h-14 min-w-0 flex-1 bg-transparent text-[15px] text-primary outline-none placeholder:text-faint"
          placeholder={t('search.placeholder')}
          value={query}
          spellCheck={false}
          data-search-input
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
              event.preventDefault();
              setActive((i) => moveActive(i, event.key === 'ArrowDown' ? 1 : -1, items.length));
            } else if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
              event.preventDefault();
              open(items[active]);
            }
          }}
        />
        <kbd className="hidden rounded-md border border-line px-1.5 py-0.5 font-mono text-[10.5px] text-subtle sm:block">Esc</kbd>
      </div>

      <div ref={list} className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-2" role="listbox" aria-label={t('search.results')}>
        {asked === '' ? (
          <p className="px-3 py-8 text-center text-[13px] text-muted">{t('search.hint')}</p>
        ) : results.error ? (
          <p className="px-3 py-8 text-center text-[13px] text-rose-300">{errorMessage(results.error)}</p>
        ) : shown && items.length === 0 && !pending ? (
          <p className="px-3 py-8 text-center text-[13px] text-muted" data-search-empty>
            {t('search.nothing', { query: asked })}
          </p>
        ) : (
          shown?.groups.map((group) => {
            const kind = kinds[group.kind] ?? { label: 'search.group.other' as MessageKey, icon: SearchIcon };
            return (
              <div key={group.kind} className="pb-2" data-search-group={group.kind}>
                <h3 className="flex items-center gap-2 px-3 pb-1 pt-2 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle">
                  {t(kind.label)}
                  {group.more && <span className="font-normal normal-case tracking-normal text-faint">{t('search.more')}</span>}
                </h3>
                {group.hits.map((hit) => {
                  const i = ++index;
                  const where = [
                    hit.project ? projectLabel(hit.project, projects.data) : group.kind === T.SearchSkills || group.kind === T.SearchConnectors ? t('search.agentboxWide') : '',
                    hit.agent && group.kind !== T.SearchAgents ? agentTitle(hit.project ?? '', hit.agent) : '',
                  ].filter(Boolean);
                  return (
                    <button
                      key={`${group.kind}:${hit.project ?? ''}:${hit.agent ?? ''}:${hit.id}`}
                      type="button"
                      role="option"
                      aria-selected={i === active}
                      data-search-hit={hit.id}
                      data-search-active={i === active || undefined}
                      className={cn('flex w-full items-start gap-3 rounded-xl px-3 py-2 text-left transition-colors', i === active ? 'bg-surface-raised' : 'hover:bg-surface-faint')}
                      onMouseMove={() => i !== active && setActive(i)}
                      onClick={() => open({ kind: group.kind, hit })}
                    >
                      <kind.icon className={cn('mt-0.5 size-4 shrink-0', i === active ? 'text-brand-300' : 'text-subtle')} />
                      <span className="grid min-w-0 flex-1 gap-0.5">
                        <span className="flex min-w-0 items-baseline gap-2">
                          <span className={cn('truncate text-[13px] text-primary', group.kind === T.SearchEvents && 'font-mono text-[12px]')}>
                            <Marked text={hit.title} query={asked} />
                          </span>
                          {hit.tag && <span className="max-w-[40%] shrink-0 truncate font-mono text-[10.5px] text-subtle">{hit.tag}</span>}
                        </span>
                        {hit.detail && (
                          <span className="line-clamp-2 break-words text-[12px] leading-snug text-muted">
                            <Marked text={hit.detail} query={asked} />
                          </span>
                        )}
                      </span>
                      <span className="flex max-w-[34%] shrink-0 flex-col items-end gap-0.5 pt-0.5 text-[11px] text-subtle">
                        {where.length > 0 && <span className="max-w-full truncate">{where.join(' · ')}</span>}
                        {hit.at && <span className="text-faint">{timeAgo(hit.at)}</span>}
                      </span>
                      {i === active && <CornerDownLeft className="mt-0.5 size-3.5 shrink-0 text-subtle" />}
                    </button>
                  );
                })}
              </div>
            );
          })
        )}
      </div>

      <div className="flex items-center gap-4 border-t border-line px-4 py-2 text-[11px] text-subtle">
        <span className="flex items-center gap-1.5">
          <kbd className="rounded border border-line px-1 font-mono">↑</kbd>
          <kbd className="rounded border border-line px-1 font-mono">↓</kbd>
          {t('search.keys.move')}
        </span>
        <span className="flex items-center gap-1.5">
          <kbd className="rounded border border-line px-1 font-mono">↵</kbd>
          {t('search.keys.open')}
        </span>
      </div>
    </>
  );
}

function Marked({ text, query }: { text: string; query: string }) {
  return (
    <>
      {highlight(text, query).map((part, i) =>
        part.match ? (
          <mark key={i} className="rounded-[3px] bg-brand-400/25 text-inherit">
            {part.text}
          </mark>
        ) : (
          <span key={i}>{part.text}</span>
        ),
      )}
    </>
  );
}
