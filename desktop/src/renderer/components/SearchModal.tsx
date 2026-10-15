import * as DialogPrimitive from '@radix-ui/react-dialog';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import {
  Archive,
  ArrowDownUp,
  Bot,
  Brain,
  CalendarDays,
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
import { useEffect, useRef, useState, type ReactNode } from 'react';
import * as T from '../../shared/api';
import { api } from '../lib/api';
import { matchesIn } from '../lib/chatFind';
import { useUiV2 } from '../lib/uiV2';
import { useFocusReturn } from '../lib/focusReturn';
import { useLanguage, useT, type MessageKey } from '../lib/i18n';
import { mediaUrl } from '../lib/media';
import { projectLabel } from '../lib/projectName';
import { highlight, moveActive, newestFirst, searchItems, searchTarget, type SearchItem, type SearchTarget } from '../lib/search';
import { closeSearch, sinceTime, useSearchModal, type SearchScope, type SearchSince } from '../lib/searchOpen';
import { countFeature } from '../lib/usageStats';
import { cn, errorMessage, timeAgo } from '../lib/utils';
import { Menu, MenuContent, MenuItem, MenuTrigger } from './ui/menu';

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
const kindOf = (kind: string) => kinds[kind] ?? { label: 'search.group.other' as MessageKey, icon: SearchIcon };

// How long the typing has to pause before the daemon is asked.
const debounceMs = 140;
// How many of one kind the modal lists once that kind's chip is picked; with
// every kind at once, each brings the daemon's own few.
const kindLimit = 50;

// isSearchShortcut is Ctrl+K or Ctrl+Shift+F (⌘ on a Mac), which open the
// search from anywhere but a terminal, where both are the shell's.
export function isSearchShortcut(event: KeyboardEvent): boolean {
  if (event.altKey || !(event.ctrlKey || event.metaKey)) return false;
  const key = event.key.toLowerCase();
  if (!((key === 'k' && !event.shiftKey) || (key === 'f' && event.shiftKey))) return false;
  return !(event.target instanceof Element && event.target.closest('.xterm'));
}

// SearchModal searches everything AgentBox keeps, in every project, while you
// type (GET /v1/search): chips that narrow it to one kind, each with how many
// it found, the project and how far back as menus, the results in one list,
// and the one you're on read in place beside it before you go there. It is
// the app's one search: App renders it, and openSearch (lib/searchOpen.ts)
// opens it from anywhere, on a scope of the caller's.
export function SearchModal({ onOpen }: { onOpen: (target: SearchTarget) => void }) {
  const t = useT();
  const { open, scope, opening } = useSearchModal();
  const focusReturn = useFocusReturn();
  return (
    <DialogPrimitive.Root open={open} onOpenChange={(next) => !next && closeSearch()}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 animate-fade-in bg-scrim backdrop-blur-[3px]" />
        <DialogPrimitive.Content
          {...focusReturn}
          aria-describedby={undefined}
          className="fixed left-1/2 top-[8vh] z-50 flex h-[80vh] w-[calc(100vw-2rem)] max-w-6xl -translate-x-1/2 animate-pop-in flex-col overflow-hidden rounded-2xl border border-line-strong bg-modal shadow-[0_40px_120px_-30px_var(--ab-shadow-deep)] focus:outline-none"
          data-search-modal
        >
          <DialogPrimitive.Title className="sr-only">{t('search.title')}</DialogPrimitive.Title>
          {open && (
            <Searching
              key={opening}
              scope={scope}
              onOpen={(target) => {
                closeSearch();
                onOpen(target);
              }}
            />
          )}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

function Searching({ scope, onOpen }: { scope: SearchScope; onOpen: (target: SearchTarget) => void }) {
  const t = useT();
  // v2 calls the Home chat Home, as its sidebar does.
  const homeName = t(useUiV2() ? 'shell.nav.home' : 'shell.nav.mainChat');
  const [query, setQuery] = useState(scope.query ?? '');
  const [asked, setAsked] = useState((scope.query ?? '').trim());
  const [kind, setKind] = useState(scope.kind ?? '');
  const [project, setProject] = useState(scope.project ?? '');
  const [since, setSince] = useState<SearchSince>(scope.since ?? '');
  const [newest, setNewest] = useState(false);
  // Past agents' chats are left out unless asked for: they open read-only.
  const [archived, setArchived] = useState(false);
  const [active, setActive] = useState(0);
  const list = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });

  useEffect(() => countFeature(T.FeatureSearchOpen), []);
  useEffect(() => {
    const timer = setTimeout(() => setAsked(query.trim()), debounceMs);
    return () => clearTimeout(timer);
  }, [query]);

  // Every kind at once, for the chips' counts and the list when no chip is
  // picked; the picked kind on its own, for more of it.
  const filters = { project, since: sinceTime(since), archived };
  const all = useQuery({
    queryKey: ['search', asked, project, since, archived],
    queryFn: () => api.search(asked, filters),
    enabled: asked !== '',
    // The last answer stays up while the next is on its way, so the list
    // doesn't blink empty at every keystroke.
    placeholderData: keepPreviousData,
    staleTime: 5_000,
  });
  const one = useQuery({
    queryKey: ['search', asked, project, since, archived, kind],
    queryFn: () => api.search(asked, { ...filters, kinds: [kind], limit: kindLimit }),
    enabled: asked !== '' && kind !== '',
    placeholderData: keepPreviousData,
    staleTime: 5_000,
  });
  const results = kind ? one : all;
  const shown = asked === '' ? undefined : results.data;
  const found = searchItems(shown);
  const items = newest ? newestFirst(found) : found;
  // Still answering what was typed: the list up is the one before it.
  const pending = query.trim() !== asked || results.isPlaceholderData;

  // A new answer starts at its first result.
  useEffect(() => setActive(0), [shown, newest]);
  useEffect(() => {
    list.current?.querySelector('[data-search-active]')?.scrollIntoView({ block: 'nearest' });
  }, [active, shown]);

  // Enter pressed before the answer to what was typed came back opens that
  // answer's first result when it comes, not the first of the one before.
  const [openWhenAnswered, setOpenWhenAnswered] = useState(false);
  useEffect(() => {
    if (!openWhenAnswered || pending || results.isFetching) return;
    setOpenWhenAnswered(false);
    if (items[0]) onOpen(searchTarget(items[0]));
  }, [openWhenAnswered, pending, results.isFetching, items, onOpen]);

  const agentTitle = (project: string, name: string) => (name === T.LeadName ? t('chat.find.from.lead') : agents.data?.find((a) => a.ref === `${project}/${name}`)?.title || name);
  const whereOf = ({ kind, hit }: SearchItem) =>
    [
      hit.project === T.HomeProject ? homeName : hit.project ? projectLabel(hit.project, projects.data) : kind === T.SearchSkills || kind === T.SearchConnectors ? t('search.agentboxWide') : '',
      hit.agent && kind !== T.SearchAgents ? agentTitle(hit.project ?? '', hit.agent) : '',
      hit.archived ? t('search.pastAgent') : '',
    ].filter(Boolean);
  // A chat message opens at itself, or, asked to find there, with the chat's
  // find bar on what was searched for.
  const open = (item: SearchItem | undefined, find = false) => item && onOpen(searchTarget(item, find && item.kind === T.SearchChats ? asked : undefined));
  const current = items[active];

  // The chips: every kind something was found of, with how many, and the
  // picked one even when it found nothing.
  const groups = (asked === '' ? [] : (all.data?.groups ?? [])).filter((g) => g.total > 0 || g.hits.length > 0 || g.kind === kind);
  if (kind && !groups.some((g) => g.kind === kind)) groups.push({ kind, hits: [], total: 0 });
  const count = (g: { total: number; capped?: boolean }) => `${g.total}${g.capped ? '+' : ''}`;
  const total = groups.reduce((n, g) => n + g.total, 0);
  const capped = groups.some((g) => g.capped);

  return (
    <>
      <div className="flex items-center gap-3 border-b border-line px-4">
        {pending && asked !== '' ? <LoaderCircle className="size-4 shrink-0 animate-spin text-subtle" /> : <SearchIcon className="size-4 shrink-0 text-subtle" />}
        <input
          ref={input}
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
              if (!pending) return open(items[active], event.shiftKey);
              setAsked(query.trim());
              setOpenWhenAnswered(true);
            }
          }}
        />
        <kbd className="hidden rounded-md border border-line px-1.5 py-0.5 font-mono text-[10.5px] text-subtle sm:block">Esc</kbd>
      </div>

      <div className="flex shrink-0 flex-wrap items-center gap-1.5 border-b border-line px-3 py-2" data-search-filters>
        <Chip on={kind === ''} onClick={() => setKind('')} data-search-kind="">
          {t('search.all')}
          {groups.length > 0 && <Count>{`${total}${capped ? '+' : ''}`}</Count>}
        </Chip>
        {groups.map((g) => {
          const k = kindOf(g.kind);
          return (
            <Chip key={g.kind} on={kind === g.kind} onClick={() => setKind(kind === g.kind ? '' : g.kind)} data-search-kind={g.kind}>
              <k.icon className="size-3.5" />
              {t(k.label)}
              <Count>{count(g)}</Count>
            </Chip>
          );
        })}
        <span className="flex-1" />
        <Chip on={archived} onClick={() => setArchived((on) => !on)} data-search-archived>
          <Archive className="size-3.5" />
          {t('search.includePast')}
        </Chip>
        <Pick
          icon={FolderGit2}
          value={project}
          onChange={setProject}
          label={t('search.project')}
          options={[
            ['', t('search.anyProject')],
            ...(projects.data ?? []).map((p): [string, string] => [p.name, projectLabel(p.name, projects.data)]),
            [T.HomeProject, homeName],
          ]}
        />
        <Pick
          icon={CalendarDays}
          value={since}
          onChange={(v) => setSince(v as SearchSince)}
          label={t('search.when')}
          options={[
            ['', t('search.since.any')],
            ['day', t('search.since.day')],
            ['week', t('search.since.week')],
            ['month', t('search.since.month')],
          ]}
        />
      </div>

      <div className="flex min-h-0 flex-1">
        <div ref={list} className="min-w-0 flex-1 overflow-y-auto overscroll-contain p-2" role="listbox" aria-label={t('search.results')}>
          {asked === '' ? (
            <p className="px-3 py-8 text-center text-[13px] text-muted">{t('search.hint')}</p>
          ) : results.error ? (
            <p className="px-3 py-8 text-center text-[13px] text-rose-300">{errorMessage(results.error)}</p>
          ) : shown && items.length === 0 && !pending ? (
            <p className="px-3 py-8 text-center text-[13px] text-muted" data-search-empty>
              {t('search.nothing', { query: asked })}
            </p>
          ) : (
            items.length > 0 && (
              <>
                <div className="flex items-center justify-between px-3 pb-1 pt-1 text-[11px] text-subtle">
                  <span>{kind ? t('search.showing', { n: items.length, total: count(groups.find((g) => g.kind === kind) ?? { total: 0 }) }) : t('search.bestOfEach')}</span>
                  <button type="button" className="flex items-center gap-1 rounded px-1 hover:text-title" onClick={() => setNewest((n) => !n)} data-search-sort>
                    <ArrowDownUp className="size-3" />
                    {newest ? t('search.sort.newest') : t('search.sort.best')}
                  </button>
                </div>
                {items.map((item, i) => {
                  const { hit } = item;
                  const k = kindOf(item.kind);
                  const where = whereOf(item);
                  return (
                    <button
                      key={`${item.kind}:${hit.project ?? ''}:${hit.agent ?? ''}:${hit.id}`}
                      type="button"
                      role="option"
                      aria-selected={i === active}
                      data-search-hit={hit.id}
                      data-search-active={i === active || undefined}
                      className={cn('flex w-full items-start gap-3 rounded-xl px-3 py-2 text-left transition-colors', i === active ? 'bg-surface-raised' : 'hover:bg-surface-faint')}
                      onClick={() => {
                        setActive(i);
                        input.current?.focus();
                      }}
                      onDoubleClick={() => open(item)}
                    >
                      <k.icon className={cn('mt-0.5 size-4 shrink-0', i === active ? 'text-brand-300' : 'text-subtle')} />
                      <span className="grid min-w-0 flex-1 gap-0.5">
                        <span className="flex min-w-0 items-baseline gap-2">
                          <span className={cn('truncate text-[13px] text-primary', item.kind === T.SearchEvents && 'font-mono text-[12px]')}>
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
                    </button>
                  );
                })}
              </>
            )
          )}
        </div>
        {current && asked !== '' && (
          <Preview item={current} query={asked} where={whereOf(current)} agentTitle={agentTitle} onOpen={(find) => open(current, find)} />
        )}
      </div>

      <div className="flex shrink-0 items-center gap-4 border-t border-line px-4 py-2 text-[11px] text-subtle">
        <span className="flex items-center gap-1.5">
          <kbd className="rounded border border-line px-1 font-mono">↑</kbd>
          <kbd className="rounded border border-line px-1 font-mono">↓</kbd>
          {t('search.keys.move')}
        </span>
        <span className="flex items-center gap-1.5">
          <kbd className="rounded border border-line px-1 font-mono">↵</kbd>
          {t('search.keys.open')}
        </span>
        {current?.kind === T.SearchChats && (
          <span className="flex items-center gap-1.5">
            <kbd className="rounded border border-line px-1 font-mono">⇧↵</kbd>
            {t('search.keys.find')}
          </span>
        )}
      </div>
    </>
  );
}

// Preview is the result you're on, read where it is: a chat message among
// the ones around it, a report or a memory whole, a screenshot as itself.
function Preview({
  item,
  query,
  where,
  agentTitle,
  onOpen,
}: {
  item: SearchItem;
  query: string;
  where: string[];
  agentTitle: (project: string, name: string) => string;
  onOpen: (find: boolean) => void;
}) {
  const t = useT();
  const language = useLanguage();
  const { kind, hit } = item;
  const k = kindOf(kind);
  return (
    <aside className="hidden w-[42%] min-w-0 shrink-0 flex-col border-l border-line bg-surface-faint md:flex" data-search-preview={kind}>
      <div className="flex items-center justify-between gap-3 border-b border-line px-4 py-2.5 text-[12px] text-subtle">
        <span className="flex min-w-0 items-center gap-1.5">
          <k.icon className="size-3.5 shrink-0" />
          <span className="truncate">{where.join(' · ') || t(k.label)}</span>
        </span>
        {hit.at && <span className="shrink-0 text-faint">{new Date(hit.at).toLocaleString(language, { dateStyle: 'medium', timeStyle: 'short' })}</span>}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4 text-[13px] leading-relaxed">
        {kind === T.SearchChats ? (
          <ChatAround hit={hit} query={query} agentTitle={agentTitle} />
        ) : kind === T.SearchReports && hit.report ? (
          <ReportPreview report={hit.report} query={query} />
        ) : kind === T.SearchMemories && hit.memory ? (
          <div className="grid gap-2">
            <div className="flex items-baseline gap-2">
              <span className="font-medium text-title">
                <Marked text={hit.memory.title} query={query} />
              </span>
              <span className="font-mono text-[10.5px] text-subtle">{hit.memory.kind}</span>
            </div>
            <p className="whitespace-pre-wrap break-words text-primary">
              <Marked text={hit.memory.content} query={query} />
            </p>
          </div>
        ) : kind === T.SearchMedia && hit.media ? (
          <div className="grid gap-3">
            {hit.media.kind === 'screenshot' && <img src={mediaUrl(hit.media)} alt={hit.media.name} className="max-h-72 w-full rounded-lg border border-line object-contain" />}
            <span className="font-medium text-title">
              <Marked text={hit.media.name} query={query} />
            </span>
            {hit.media.text && (
              <p className="whitespace-pre-wrap break-words text-muted">
                <Marked text={hit.media.text} query={query} />
              </p>
            )}
          </div>
        ) : (
          <div className="grid gap-2">
            <span className={cn('font-medium text-title', kind === T.SearchEvents && 'font-mono text-[12px]')}>
              <Marked text={hit.title} query={query} />
            </span>
            {hit.detail && (
              <p className="whitespace-pre-wrap break-words text-muted">
                <Marked text={hit.detail} query={query} />
              </p>
            )}
          </div>
        )}
      </div>
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-t border-line px-4 py-2.5">
        <button type="button" className="flex items-center gap-1.5 rounded-lg bg-brand-500 px-2.5 py-1 text-[12px] font-medium text-white hover:bg-brand-400" onClick={() => onOpen(false)} data-search-open>
          {kind === T.SearchChats ? t('search.openInChat') : t('search.open.item')}
          <kbd className="font-mono text-[10.5px] opacity-80">↵</kbd>
        </button>
        {kind === T.SearchChats && (
          <button type="button" className="flex items-center gap-1.5 rounded-lg border border-line px-2.5 py-1 text-[12px] text-muted hover:text-title" onClick={() => onOpen(true)} data-search-find>
            {t('search.findInChat')}
            <kbd className="font-mono text-[10.5px] text-subtle">⇧↵</kbd>
          </button>
        )}
      </div>
    </aside>
  );
}

// How many of a chat's messages the preview reads around the one found.
const aroundLimit = 7;

// ChatAround is a chat message among the few around it, as the chat shows
// them: yours on the right, the AI tool's on the left, the one found ringed
// and what was searched for marked in it.
function ChatAround({ hit, query, agentTitle }: { hit: T.SearchHit; query: string; agentTitle: (project: string, name: string) => string }) {
  const t = useT();
  const project = hit.project ?? '';
  const ref = `${project}/${hit.agent || T.LeadName}`;
  const page = useQuery({ queryKey: ['searchAround', ref, hit.id], queryFn: () => api.chat(ref, { around: hit.id, limit: aroundLimit }), staleTime: 30_000 });
  const found = useRef<HTMLDivElement>(null);
  useEffect(() => {
    found.current?.scrollIntoView({ block: 'center' });
  }, [page.data]);
  if (page.isPending) return <LoaderCircle className="size-4 animate-spin text-subtle" />;
  if (page.error) return <p className="text-rose-300">{errorMessage(page.error)}</p>;
  const said = page.data.items.filter((it) => ['user', 'aside', 'assistant', 'notice', 'error'].includes(it.kind) && it.text && !it.hidden);
  const ai = agentTitle(project, hit.agent || T.LeadName);
  return (
    <div className="grid content-start gap-3">
      {said.map((it) => {
        const mine = it.kind === 'user' || it.kind === 'aside';
        const here = it.id === hit.id;
        return (
          <div key={it.id} ref={here ? found : undefined} className={cn('grid gap-1', mine && 'justify-items-end')} data-search-around={here || undefined}>
            <span className="px-1 text-[10.5px] text-subtle">{mine ? t('chat.find.from.you') : it.kind === 'assistant' ? ai : t('chat.find.notice')}</span>
            <div
              className={cn(
                'max-w-[92%] whitespace-pre-wrap break-words rounded-xl px-3 py-2',
                mine ? 'bg-surface-raised text-primary' : 'text-primary',
                here ? 'ring-1 ring-amber-500/60' : 'line-clamp-4 text-muted',
              )}
            >
              {here ? <Found text={it.text!} query={query} /> : it.text}
            </div>
          </div>
        );
      })}
    </div>
  );
}

// Found marks query in a chat message the way the chat's find does: as one
// piece, ignoring case and accents.
function Found({ text, query }: { text: string; query: string }) {
  const parts: ReactNode[] = [];
  let last = 0;
  for (const [start, end] of matchesIn(text, query)) {
    parts.push(text.slice(last, start), <mark key={start} className="rounded-[3px] bg-amber-500 text-stone-900">{text.slice(start, end)}</mark>);
    last = end;
  }
  parts.push(text.slice(last));
  return <>{parts}</>;
}

function ReportPreview({ report, query }: { report: T.AgentReport; query: string }) {
  const t = useT();
  const lists: [MessageKey, string[] | undefined][] = [
    ['search.report.discoveries', report.discoveries],
    ['search.report.decisions', report.decisions],
    ['search.report.remaining', report.remainingIssues],
  ];
  return (
    <div className="grid gap-3">
      <div className="flex items-baseline gap-2">
        <span className="font-medium text-title">
          <Marked text={report.task || report.summary} query={query} />
        </span>
        <span className="font-mono text-[10.5px] text-subtle">{report.status}</span>
      </div>
      <p className="whitespace-pre-wrap break-words text-primary">
        <Marked text={report.summary} query={query} />
      </p>
      {lists.map(
        ([label, lines]) =>
          lines &&
          lines.length > 0 && (
            <div key={label} className="grid gap-1">
              <h4 className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle">{t(label)}</h4>
              <ul className="grid list-disc gap-1 pl-4 text-muted">
                {lines.map((line) => (
                  <li key={line}>
                    <Marked text={line} query={query} />
                  </li>
                ))}
              </ul>
            </div>
          ),
      )}
    </div>
  );
}

function Chip({ on, onClick, children, ...rest }: { on: boolean; onClick: () => void; children: ReactNode; [data: `data-${string}`]: string }) {
  return (
    <button
      type="button"
      aria-pressed={on}
      onClick={onClick}
      className={cn(
        'inline-flex shrink-0 items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11.5px] transition-colors',
        on ? 'border-brand-400/40 bg-brand-400/15 text-brand-200' : 'border-line text-muted hover:border-line-strong hover:text-title',
      )}
      {...rest}
    >
      {children}
    </button>
  );
}

function Count({ children }: { children: string }) {
  return <span className="tabular-nums opacity-70">{children}</span>;
}

// Pick is a filter's menu, as a chip: what it is set to, and the choices.
function Pick({ icon: Icon, value, onChange, label, options }: { icon: LucideIcon; value: string; onChange: (value: string) => void; label: string; options: [string, string][] }) {
  const current = options.find(([v]) => v === value) ?? options[0];
  return (
    <Menu>
      <MenuTrigger asChild>
        <button
          type="button"
          aria-label={label}
          className={cn(
            'inline-flex max-w-52 shrink-0 items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11.5px] transition-colors',
            value ? 'border-brand-400/40 bg-brand-400/15 text-brand-200' : 'border-line text-muted hover:border-line-strong hover:text-title',
          )}
        >
          <Icon className="size-3.5 shrink-0" />
          <span className="truncate">{current[1]}</span>
        </button>
      </MenuTrigger>
      <MenuContent align="end" className="max-h-80 overflow-y-auto">
        {options.map(([v, text]) => (
          <MenuItem key={v} onSelect={() => onChange(v)}>
            <span className={cn(v === value && 'text-brand-300')}>{text}</span>
          </MenuItem>
        ))}
      </MenuContent>
    </Menu>
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
