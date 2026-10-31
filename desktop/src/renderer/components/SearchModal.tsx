import * as DialogPrimitive from '@radix-ui/react-dialog';
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Archive,
  ArrowDownUp,
  ChevronRight,
  SlidersHorizontal,
  Zap,
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
import { useEffect, useRef, useState, type MutableRefObject, type ReactNode } from 'react';
import * as T from '../../shared/api';
import { api } from '../lib/api';
import { matchesIn } from '../lib/chatFind';
import { plainText } from '../lib/plainText';
import { useUiV2 } from '../lib/uiV2';
import { useFocusReturn } from '../lib/focusReturn';
import { useLanguage, useT, type MessageKey } from '../lib/i18n';
import { mediaUrl } from '../lib/media';
import { projectLabel } from '../lib/projectName';
import { highlight, moveActive, newestFirst, searchItems, searchTarget, type SearchItem, type SearchTarget } from '../lib/search';
import { commandsOnly, looksLikeVerb, paletteRows, pickChoices, rankCommands, type PaletteRow } from '../lib/palette';
import { closeSearch, sinceTime, useSearchModal, type SearchScope, type SearchSince } from '../lib/searchOpen';
import { countFeature } from '../lib/usageStats';
import { cn, errorMessage, onMac, timeAgo } from '../lib/utils';
import { currentAgent, currentProject, runSetting, usePaletteCommands, type PaletteAction, type PaletteCommand, type PaletteContext } from './paletteCommands';
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
//
// It is the command palette too: actions (verbs with a handler each) and every
// setting, matched as you type (lib/palette.ts, components/paletteCommands.tsx),
// above what was found when what you typed starts with a verb, below it
// otherwise, and alone after ">". An action for an agent or a project runs on
// the one you're on, or asks for one in the palette.
export function SearchModal({ onOpen, palette }: { onOpen: (target: SearchTarget) => void; palette: PaletteContext }) {
  const t = useT();
  const { open, scope, opening } = useSearchModal();
  const focusReturn = useFocusReturn();
  // Escape steps back out of a pick first, and only then closes.
  const back = useRef<(() => boolean) | null>(null);
  return (
    <DialogPrimitive.Root open={open} onOpenChange={(next) => !next && closeSearch()}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 animate-fade-in bg-scrim backdrop-blur-[3px]" />
        <DialogPrimitive.Content
          {...focusReturn}
          onEscapeKeyDown={(event) => back.current?.() && event.preventDefault()}
          aria-describedby={undefined}
          className="fixed left-1/2 top-[8vh] z-50 flex h-[80vh] w-[calc(100vw-2rem)] max-w-6xl -translate-x-1/2 animate-pop-in flex-col overflow-hidden rounded-2xl border border-line-strong bg-modal shadow-[0_40px_120px_-30px_var(--ab-shadow-deep)] focus:outline-none"
          data-search-modal
        >
          <DialogPrimitive.Title className="sr-only">{t('search.title')}</DialogPrimitive.Title>
          {open && (
            <Searching
              key={opening}
              scope={scope}
              palette={palette}
              back={back}
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

// The palette's own chips, beside the daemon's kinds.
const actionsKind = '@actions';
const settingsKind = '@settings';
const commandKinds: Record<string, { label: MessageKey; icon: LucideIcon; group: 'actions' | 'settings' }> = {
  [actionsKind]: { label: 'palette.group.actions', icon: Zap, group: 'actions' },
  [settingsKind]: { label: 'palette.group.settings', icon: SlidersHorizontal, group: 'settings' },
};

type Row = PaletteRow<PaletteCommand, SearchItem>;
const rowKey = (row: Row) => (row.kind === 'command' ? `command:${row.command.id}` : `${row.item.kind}:${row.item.hit.project ?? ''}:${row.item.hit.agent ?? ''}:${row.item.hit.id}`);
const keyName = (k: string) => (k === 'Mod' ? (onMac ? '⌘' : 'Ctrl') : k);

function Searching({ scope, palette, back, onOpen }: { scope: SearchScope; palette: PaletteContext; back: MutableRefObject<(() => boolean) | null>; onOpen: (target: SearchTarget) => void }) {
  const t = useT();
  // v2 calls the Home chat Home, as its sidebar does.
  const homeName = t(useUiV2() ? 'shell.nav.home' : 'shell.nav.mainChat');
  const [query, setQuery] = useState(scope.query ?? '');
  const [asked, setAsked] = useState(commandsOnly(scope.query ?? '').only ? '' : (scope.query ?? '').trim());
  const [kind, setKind] = useState(scope.kind ?? '');
  const [project, setProject] = useState(scope.project ?? '');
  const [since, setSince] = useState<SearchSince>(scope.since ?? '');
  const [newest, setNewest] = useState(false);
  // Past agents' chats are left out unless asked for: they open read-only.
  const [archived, setArchived] = useState(false);
  const [active, setActive] = useState(0);
  // An action that asked which agent or project it's for, and what's typed
  // to find one.
  const [picking, setPicking] = useState<PaletteAction | null>(null);
  const [pickQuery, setPickQuery] = useState('');
  const list = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const commands = usePaletteCommands(palette, useQueryClient());
  const verbs = t('palette.verbs').split(/\s+/);

  useEffect(() => countFeature(T.FeatureSearchOpen), []);
  useEffect(() => {
    // ">" asks for commands only: nothing to ask the daemon.
    const timer = setTimeout(() => setAsked(commandsOnly(query).only ? '' : query.trim()), debounceMs);
    return () => clearTimeout(timer);
  }, [query]);
  back.current = () => {
    if (!picking) return false;
    setPicking(null);
    input.current?.focus();
    return true;
  };

  // Every kind at once, for the chips' counts and the list when no chip is
  // picked; the picked kind on its own, for more of it.
  const daemonKind = commandKinds[kind] ? '' : kind;
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
    queryKey: ['search', asked, project, since, archived, daemonKind],
    queryFn: () => api.search(asked, { ...filters, kinds: [daemonKind], limit: kindLimit }),
    enabled: asked !== '' && daemonKind !== '',
    placeholderData: keepPreviousData,
    staleTime: 5_000,
  });
  const results = daemonKind ? one : all;
  const shown = asked === '' ? undefined : results.data;
  const found = searchItems(shown);
  const items = newest ? newestFirst(found) : found;
  const only = commandsOnly(query).only;
  // Still answering what was typed: the list up is the one before it.
  const pending = !only && (query.trim() !== asked || results.isPlaceholderData);

  // The commands answer at once, from what's typed now. With nothing typed,
  // the actions are the list, settings being too many to list unasked.
  const ranked = rankCommands(commands, query);
  const typed = commandsOnly(query).rest !== '';
  const rankedActions = ranked.filter((r) => r.command.group === 'actions');
  const rankedSettings = ranked.filter((r) => r.command.group === 'settings');
  const rows: Row[] = commandKinds[kind]
    ? ranked.filter((r) => r.command.group === commandKinds[kind].group).map((r) => ({ kind: 'command', command: r.command }))
    : kind
      ? items.map((item) => ({ kind: 'found', item }))
      : !typed
        ? rankedActions.map((r) => ({ kind: 'command', command: r.command }))
        : paletteRows(ranked, items, query, verbs);
  const commandsFirst = only || !!commandKinds[kind] || !typed || looksLikeVerb(query, verbs);

  // What a pick offers: the agents (not a lead) or the projects, the one
  // you're on first.
  const pickCurrent = picking?.needs === 'agent' ? currentAgent(palette.view) : currentProject(palette.view);
  const choices = !picking
    ? []
    : pickChoices(
        picking.needs === 'agent'
          ? (agents.data ?? []).filter((a) => a.name !== T.LeadName).map((a) => ({ id: a.ref, title: a.title || a.name, detail: `${a.name} · ${projectLabel(a.project, projects.data)}` }))
          : (projects.data ?? []).map((p) => ({ id: p.name, title: projectLabel(p.name, projects.data), detail: p.name })),
        pickQuery,
        pickCurrent,
      );
  const count = picking ? choices.length : rows.length;

  // A new answer starts at its first result.
  useEffect(() => setActive(0), [shown, newest, kind, query, picking, pickQuery]);
  useEffect(() => {
    list.current?.querySelector('[data-search-active]')?.scrollIntoView({ block: 'nearest' });
  }, [active, shown]);

  const run = (command: PaletteCommand, other = false) => {
    if (command.group === 'settings') {
      closeSearch();
      return runSetting(command);
    }
    const current = command.needs === 'agent' ? currentAgent(palette.view) : command.needs === 'project' ? currentProject(palette.view) : '';
    if (command.needs && (other || !current)) {
      setPicking(command);
      setPickQuery('');
      return;
    }
    closeSearch();
    void command.run(current ?? '');
  };
  const pick = (id: string | undefined) => {
    if (!picking || !id) return;
    closeSearch();
    void picking.run(id);
  };

  // Enter pressed before the answer to what was typed came back opens that
  // answer's first row when it comes, not the first of the one before.
  const [openWhenAnswered, setOpenWhenAnswered] = useState(false);
  useEffect(() => {
    if (!openWhenAnswered || pending || results.isFetching) return;
    setOpenWhenAnswered(false);
    const first = rows[0];
    if (first?.kind === 'command') run(first.command);
    else if (first) onOpen(searchTarget(first.item));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openWhenAnswered, pending, results.isFetching, rows, onOpen]);

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
  const go = (row: Row | undefined, shift = false) => (row?.kind === 'command' ? run(row.command, shift) : open(row?.item, shift));
  const current = rows[active];
  const currentItem = current?.kind === 'found' ? current.item : undefined;
  const currentCommand = current?.kind === 'command' ? current.command : undefined;

  // The chips: the actions and settings that answer, every kind something
  // was found of, with how many, and the picked one even when it found
  // nothing.
  const groups = (asked === '' ? [] : (all.data?.groups ?? [])).filter((g) => g.total > 0 || g.hits.length > 0 || g.kind === kind);
  if (daemonKind && !groups.some((g) => g.kind === daemonKind)) groups.push({ kind: daemonKind, hits: [], total: 0 });
  const countOf = (g: { total: number; capped?: boolean }) => `${g.total}${g.capped ? '+' : ''}`;
  const total = groups.reduce((n, g) => n + g.total, 0) + (typed ? ranked.length : 0);
  const capped = groups.some((g) => g.capped);
  const commandChips = (
    [
      [actionsKind, rankedActions.length],
      [settingsKind, typed ? rankedSettings.length : 0],
    ] as const
  ).filter(([k, n]) => n > 0 || kind === k);
  // Tab walks the chips, as the arrows walk the rows.
  const chips = ['', ...commandChips.map(([k]) => k), ...groups.map((g) => g.kind)];

  const actionFor = (command: PaletteCommand) => {
    if (command.group !== 'actions' || !command.needs) return undefined;
    const ref = command.needs === 'agent' ? currentAgent(palette.view) : currentProject(palette.view);
    if (!ref) return t(command.needs === 'agent' ? 'palette.asksAgent' : 'palette.asksProject');
    return command.needs === 'agent' ? agentTitle(ref.split('/')[0], ref.split('/')[1]) : projectLabel(ref, projects.data);
  };

  return (
    <>
      <div className="flex items-center gap-3 border-b border-line px-4">
        {pending && asked !== '' && !picking ? <LoaderCircle className="size-4 shrink-0 animate-spin text-subtle" /> : <SearchIcon className="size-4 shrink-0 text-subtle" />}
        {picking && (
          <button
            type="button"
            className="flex max-w-[45%] shrink-0 items-center gap-1 rounded-full border border-brand-400/40 bg-brand-400/15 px-2.5 py-1 text-[11.5px] text-brand-200"
            onClick={() => back.current?.()}
            data-palette-picking={picking.id}
          >
            <picking.icon className="size-3.5 shrink-0" />
            <span className="truncate">{picking.title}</span>
            <ChevronRight className="size-3.5 shrink-0 opacity-70" />
          </button>
        )}
        <input
          ref={input}
          autoFocus
          aria-label={t('search.label')}
          className="h-14 min-w-0 flex-1 bg-transparent text-[15px] text-primary outline-none placeholder:text-faint"
          placeholder={picking ? t(picking.needs === 'agent' ? 'palette.pick.agent' : 'palette.pick.project') : t('search.placeholder')}
          value={picking ? pickQuery : query}
          spellCheck={false}
          data-search-input
          onChange={(event) => (picking ? setPickQuery(event.target.value) : setQuery(event.target.value))}
          onKeyDown={(event) => {
            if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
              event.preventDefault();
              setActive((i) => moveActive(i, event.key === 'ArrowDown' ? 1 : -1, count));
            } else if (event.key === 'Tab' && !picking && chips.length > 1) {
              event.preventDefault();
              setKind(chips[moveActive(Math.max(0, chips.indexOf(kind)), event.shiftKey ? -1 : 1, chips.length)]);
            } else if (event.key === 'Backspace' && picking && pickQuery === '') {
              event.preventDefault();
              back.current?.();
            } else if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
              event.preventDefault();
              if (picking) return pick(choices[active]?.id);
              // Commands answer at once; what the daemon finds is waited for.
              if (!pending || (commandsFirst && current?.kind === 'command')) return go(current, event.shiftKey);
              setAsked(query.trim());
              setOpenWhenAnswered(true);
            }
          }}
        />
        <kbd className="hidden rounded-md border border-line px-1.5 py-0.5 font-mono text-[10.5px] text-subtle sm:block">Esc</kbd>
      </div>

      {!picking && (
        <div className="flex shrink-0 flex-wrap items-center gap-1.5 border-b border-line px-3 py-2" data-search-filters>
          <Chip on={kind === ''} onClick={() => setKind('')} data-search-kind="">
            {t('search.all')}
            {(groups.length > 0 || typed) && <Count>{`${total}${capped ? '+' : ''}`}</Count>}
          </Chip>
          {commandChips.map(([k, n]) => {
            const c = commandKinds[k];
            return (
              <Chip key={k} on={kind === k} onClick={() => setKind(kind === k ? '' : k)} data-search-kind={k}>
                <c.icon className="size-3.5" />
                {t(c.label)}
                <Count>{String(n)}</Count>
              </Chip>
            );
          })}
          {groups.map((g) => {
            const k = kindOf(g.kind);
            return (
              <Chip key={g.kind} on={kind === g.kind} onClick={() => setKind(kind === g.kind ? '' : g.kind)} data-search-kind={g.kind}>
                <k.icon className="size-3.5" />
                {t(k.label)}
                <Count>{countOf(g)}</Count>
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
      )}

      <div className="flex min-h-0 flex-1">
        <div ref={list} className="min-w-0 flex-1 overflow-y-auto overscroll-contain p-2" role="listbox" aria-label={t('search.results')}>
          {picking ? (
            choices.length === 0 ? (
              <p className="px-3 py-8 text-center text-[13px] text-muted" data-search-empty>
                {t(picking.needs === 'agent' ? 'palette.pick.noAgent' : 'palette.pick.noProject')}
              </p>
            ) : (
              choices.map((c, i) => (
                <button
                  key={c.id}
                  type="button"
                  role="option"
                  aria-selected={i === active}
                  data-palette-choice={c.id}
                  data-search-active={i === active || undefined}
                  className={cn('flex w-full items-center gap-3 rounded-xl px-3 py-2 text-left transition-colors', i === active ? 'bg-surface-raised' : 'hover:bg-surface-faint')}
                  onClick={() => pick(c.id)}
                >
                  {picking.needs === 'agent' ? <Bot className="size-4 shrink-0 text-subtle" /> : <FolderGit2 className="size-4 shrink-0 text-subtle" />}
                  <span className="min-w-0 flex-1 truncate text-[13px] text-primary">
                    <Marked text={c.title} query={pickQuery} />
                  </span>
                  <span className="max-w-[40%] shrink-0 truncate text-[11px] text-subtle">{c.id === pickCurrent ? t('palette.pick.current') : c.detail}</span>
                </button>
              ))
            )
          ) : asked === '' && !typed && rows.length === 0 ? (
            <p className="px-3 py-8 text-center text-[13px] text-muted">{t('search.hint')}</p>
          ) : results.error && rows.length === 0 ? (
            <p className="px-3 py-8 text-center text-[13px] text-rose-300">{errorMessage(results.error)}</p>
          ) : rows.length === 0 && (only || !!commandKinds[kind] || (shown && !pending)) ? (
            <p className="px-3 py-8 text-center text-[13px] text-muted" data-search-empty>
              {t('search.nothing', { query: commandsOnly(query).rest || query })}
            </p>
          ) : (
            rows.length > 0 && (
              <>
                {!typed && !kind && <p className="px-3 pb-2 pt-1 text-[11px] text-subtle">{t('palette.hint')}</p>}
                {rows.map((row, i) => {
                  const previous = rows[i - 1];
                  const groupOf = (r: Row | undefined) => (r ? (r.kind === 'command' ? r.command.group : 'found') : '');
                  const header = groupOf(row) !== groupOf(previous);
                  return (
                    <div key={rowKey(row)}>
                      {header &&
                        (row.kind === 'command' ? (
                          <div className="px-3 pb-1 pt-2 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle" data-palette-group={row.command.group}>
                            {t(row.command.group === 'actions' ? 'palette.group.actions' : 'palette.group.settings')}
                          </div>
                        ) : (
                          <div className="flex items-center justify-between px-3 pb-1 pt-2 text-[11px] text-subtle">
                            <span>{kind ? t('search.showing', { n: items.length, total: countOf(groups.find((g) => g.kind === kind) ?? { total: 0 }) }) : t('search.bestOfEach')}</span>
                            <button type="button" className="flex items-center gap-1 rounded px-1 hover:text-title" onClick={() => setNewest((n) => !n)} data-search-sort>
                              <ArrowDownUp className="size-3" />
                              {newest ? t('search.sort.newest') : t('search.sort.best')}
                            </button>
                          </div>
                        ))}
                      {row.kind === 'command' ? (
                        <CommandRow
                          command={row.command}
                          query={commandsOnly(query).rest}
                          on={i === active}
                          forWhom={actionFor(row.command)}
                          onClick={() => {
                            setActive(i);
                            input.current?.focus();
                          }}
                          onRun={() => run(row.command)}
                        />
                      ) : (
                        <FoundRow
                          item={row.item}
                          query={asked}
                          on={i === active}
                          where={whereOf(row.item)}
                          onClick={() => {
                            setActive(i);
                            input.current?.focus();
                          }}
                          onOpen={() => open(row.item)}
                        />
                      )}
                    </div>
                  );
                })}
              </>
            )
          )}
        </div>
        {!picking && currentItem && asked !== '' && (
          <Preview item={currentItem} query={asked} where={whereOf(currentItem)} agentTitle={agentTitle} onOpen={(find) => open(currentItem, find)} />
        )}
        {!picking && currentCommand && <CommandPreview command={currentCommand} forWhom={actionFor(currentCommand)} onRun={(other) => run(currentCommand, other)} />}
      </div>

      <div className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-1 border-t border-line px-4 py-2 text-[11px] text-subtle">
        <span className="flex items-center gap-1.5">
          <kbd className="rounded border border-line px-1 font-mono">↑</kbd>
          <kbd className="rounded border border-line px-1 font-mono">↓</kbd>
          {t('search.keys.move')}
        </span>
        <span className="flex items-center gap-1.5">
          <kbd className="rounded border border-line px-1 font-mono">↵</kbd>
          {picking || currentCommand?.group === 'actions' ? t('palette.keys.run') : t('search.keys.open')}
        </span>
        {currentItem?.kind === T.SearchChats && !picking && (
          <span className="flex items-center gap-1.5">
            <kbd className="rounded border border-line px-1 font-mono">⇧↵</kbd>
            {t('search.keys.find')}
          </span>
        )}
        {currentCommand?.group === 'actions' && currentCommand.needs && !picking && (
          <span className="flex items-center gap-1.5">
            <kbd className="rounded border border-line px-1 font-mono">⇧↵</kbd>
            {t(currentCommand.needs === 'agent' ? 'palette.keys.otherAgent' : 'palette.keys.otherProject')}
          </span>
        )}
        {picking ? (
          <span className="flex items-center gap-1.5">
            <kbd className="rounded border border-line px-1 font-mono">Esc</kbd>
            {t('palette.keys.back')}
          </span>
        ) : (
          <>
            <span className="flex items-center gap-1.5">
              <kbd className="rounded border border-line px-1 font-mono">⇥</kbd>
              {t('palette.keys.filter')}
            </span>
            <span className="flex items-center gap-1.5">
              <kbd className="rounded border border-line px-1 font-mono">&gt;</kbd>
              {t('palette.keys.commandsOnly')}
            </span>
          </>
        )}
      </div>
    </>
  );
}

// CommandRow is an action or a setting in the list: what it does, where it
// lives or whom it's for, and its own shortcut.
function CommandRow({ command, query, on, forWhom, onClick, onRun }: { command: PaletteCommand; query: string; on: boolean; forWhom?: string; onClick: () => void; onRun: () => void }) {
  return (
    <button
      type="button"
      role="option"
      aria-selected={on}
      data-palette-command={command.id}
      data-search-active={on || undefined}
      className={cn('flex w-full items-center gap-3 rounded-xl px-3 py-2 text-left transition-colors', on ? 'bg-surface-raised' : 'hover:bg-surface-faint')}
      onClick={onClick}
      onDoubleClick={onRun}
    >
      <command.icon className={cn('size-4 shrink-0', on ? 'text-brand-300' : 'text-subtle')} />
      <span className="min-w-0 flex-1 truncate text-[13px] text-primary">
        <Marked text={command.title} query={query} />
      </span>
      {(command.where || forWhom) && <span className="max-w-[40%] shrink-0 truncate text-[11px] text-subtle">{command.where ?? forWhom}</span>}
      {command.group === 'actions' && command.keys && (
        <span className="flex shrink-0 gap-0.5">
          {command.keys.map((k) => (
            <kbd key={k} className="rounded border border-line px-1 font-mono text-[10.5px] text-subtle">
              {keyName(k)}
            </kbd>
          ))}
        </span>
      )}
    </button>
  );
}

// CommandPreview is the command you're on, said in full: what it does, where
// or on whom, and the keys that run it.
function CommandPreview({ command, forWhom, onRun }: { command: PaletteCommand; forWhom?: string; onRun: (other: boolean) => void }) {
  const t = useT();
  const action = command.group === 'actions' ? command : undefined;
  return (
    <aside className="hidden w-[42%] min-w-0 shrink-0 flex-col border-l border-line bg-surface-faint md:flex" data-palette-preview={command.id}>
      <div className="flex items-center gap-1.5 border-b border-line px-4 py-2.5 text-[12px] text-subtle">
        {action ? <Zap className="size-3.5 shrink-0" /> : <SlidersHorizontal className="size-3.5 shrink-0" />}
        <span className="truncate">{action ? t('palette.group.actions') : (command.where ?? t('palette.group.settings'))}</span>
      </div>
      <div className="grid min-h-0 flex-1 content-start gap-3 overflow-y-auto px-4 py-4 text-[13px] leading-relaxed">
        <span className="flex items-center gap-2 font-medium text-title">
          <command.icon className="size-4 shrink-0 text-brand-300" />
          {command.title}
        </span>
        <p className="text-muted">{action ? t(`palette.${action.id}.about` as MessageKey) : t('palette.about.setting')}</p>
        {command.keywords && !action && <p className="line-clamp-4 text-[12px] text-subtle">{command.keywords}</p>}
        {forWhom && (
          <p className="text-[12px] text-subtle" data-palette-for>
            {action?.needs === 'agent' ? t('palette.for.agent', { name: forWhom }) : t('palette.for.project', { name: forWhom })}
          </p>
        )}
      </div>
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-t border-line px-4 py-2.5">
        <button type="button" className="flex items-center gap-1.5 rounded-lg bg-brand-500 px-2.5 py-1 text-[12px] font-medium text-white hover:bg-brand-400" onClick={() => onRun(false)} data-palette-run>
          {action ? t('palette.run') : t('palette.openSetting')}
          <kbd className="font-mono text-[10.5px] opacity-80">↵</kbd>
        </button>
        {action?.needs && (
          <button type="button" className="flex items-center gap-1.5 rounded-lg border border-line px-2.5 py-1 text-[12px] text-muted hover:text-title" onClick={() => onRun(true)} data-palette-other>
            {t(action.needs === 'agent' ? 'palette.keys.otherAgent' : 'palette.keys.otherProject')}
            <kbd className="font-mono text-[10.5px] text-subtle">⇧↵</kbd>
          </button>
        )}
      </div>
    </aside>
  );
}

// FoundRow is one thing the daemon found.
function FoundRow({ item, query, on, where, onClick, onOpen }: { item: SearchItem; query: string; on: boolean; where: string[]; onClick: () => void; onOpen: () => void }) {
  const { hit } = item;
  const k = kindOf(item.kind);
  return (
    <button
      type="button"
      role="option"
      aria-selected={on}
      data-search-hit={hit.id}
      data-search-active={on || undefined}
      className={cn('flex w-full items-start gap-3 rounded-xl px-3 py-2 text-left transition-colors', on ? 'bg-surface-raised' : 'hover:bg-surface-faint')}
      onClick={onClick}
      onDoubleClick={onOpen}
    >
      <k.icon className={cn('mt-0.5 size-4 shrink-0', on ? 'text-brand-300' : 'text-subtle')} />
      <span className="grid min-w-0 flex-1 gap-0.5">
        <span className="flex min-w-0 items-baseline gap-2">
          <span className={cn('truncate text-[13px] text-primary', item.kind === T.SearchEvents && 'font-mono text-[12px]')}>
            <Marked text={plainText(hit.title)} query={query} />
          </span>
          {hit.tag && <span className="max-w-[40%] shrink-0 truncate font-mono text-[10.5px] text-subtle">{hit.tag}</span>}
        </span>
        {hit.detail && (
          <span className="line-clamp-2 break-words text-[12px] leading-snug text-muted">
            <Marked text={plainText(hit.detail)} query={query} />
          </span>
        )}
      </span>
      <span className="flex max-w-[34%] shrink-0 flex-col items-end gap-0.5 pt-0.5 text-[11px] text-subtle">
        {where.length > 0 && <span className="max-w-full truncate">{where.join(' · ')}</span>}
        {hit.at && <span className="text-faint">{timeAgo(hit.at)}</span>}
      </span>
    </button>
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
