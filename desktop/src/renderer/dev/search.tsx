// Design mockups for finding things, for choosing between before any of it is
// built: three ways to find in the lead's chat (Ctrl+F) and three layouts
// of the modal that searches everything (Ctrl+Shift+F). Static: nothing here searches, and the
// bars, lists and panels are drawn in the app's own classes over the app's
// real Sidebar, TopBar, ProjectView and AgentRail, against a lead chat seeded
// with a long conversation. The marks in the chat are the real FindBar's
// (CSS Custom Highlight API, rangesIn), placed by hand.
//
//   ?search=find-a     A pill (as shipped) with match options and match ticks
//                      along the scroll track, history not loaded included
//   ?search=find-b     A strip docked at the chat's top, with a list of every
//                      matching message, grouped by day
//   ?search=find-c     Find takes over the composer, with a timeline of the
//                      conversation's matches above it
//   ?search=modal-a    The search modal, one column grouped by kind
//   ?search=modal-b    The search modal, wide: a ranked list and a preview
//   ?search=modal-c    The search modal: facets, results grouped by place
//   ?search=modal-scoped  modal-a as another part of the app opens it, its
//                      scope set (the project's Memory tab)
//
// Their strings live here, en-US and pt-BR, not in the app's catalogs: they
// are proposals, and the one chosen moves its strings there.
import { useQueryClient } from '@tanstack/react-query';
import {
  ArrowDownUp,
  Bot,
  Brain,
  CalendarDays,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  CornerDownLeft,
  FileText,
  FolderGit2,
  Image,
  ListChecks,
  MessageSquare,
  Search,
  X,
  type LucideIcon,
} from 'lucide-react';
import { lazy, Suspense, useEffect, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import type * as T from '../../shared/api';
import { AgentRail } from '../components/AgentRail';
import { rangesIn } from '../components/chat/reveal';
import { Sidebar } from '../components/Sidebar';
import { TopBar } from '../components/TopBar';
import { useLanguage, useT } from '../lib/i18n';
import { cn } from '../lib/utils';
import { PROJECT } from './fixtures';

const ProjectView = lazy(() => import('../components/ProjectView').then((m) => ({ default: m.ProjectView })));

const QUERY = 'retention';
const LEAD = `${PROJECT}/lead`;
const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

// ---------------------------------------------------------------- strings

const en = {
  findA: 'A · Find pill with match ticks',
  findB: 'B · Docked find strip with a results list',
  findC: 'C · Find in the composer, with a timeline',
  modalA: 'Modal A · One column, grouped by kind',
  modalB: 'Modal B · Ranked list with a preview',
  modalC: 'Modal C · Facets, grouped by where it lives',
  modalScoped: 'Modal A, opened from the Memory tab: scoped to it',
  findThere: 'Find in that chat',
  matchCase: 'Match case',
  wholeWord: 'Whole word',
  from: 'From',
  fromAll: 'Anyone',
  fromYou: 'You',
  fromLead: 'Lead',
  fromAgents: 'Agents',
  you: 'You',
  lead: 'Lead',
  notLoaded: '{n} earlier, not loaded yet',
  notLoadedHint: 'Jumping to one loads it.',
  results: '{n} messages',
  hideResults: 'Hide list',
  today: 'Today',
  yesterday: 'Yesterday',
  earlier: 'October 3',
  findInChat: 'Find in this chat',
  backToComposer: 'Back to writing',
  conversation: 'This conversation',
  started: 'Started Oct 1',
  now: 'Now',
  searchEverything: 'Search all projects…',
  all: 'All',
  chats: 'Chats',
  reports: 'Reports',
  memory: 'Memory',
  media: 'Media',
  tasks: 'Tasks',
  events: 'Events',
  anyProject: 'Every project',
  anyTime: 'Any time',
  anyone: 'Anyone',
  openAll: 'see all results',
  toFilter: 'to filter',
  more: '{n} more',
  newKind: 'new',
  bestFirst: 'Best match',
  newestFirst: 'Newest',
  kinds: 'What',
  projects: 'Projects',
  agents: 'Agents',
  when: 'When',
  lastWeek: 'Past 7 days',
  lastMonth: 'Past 30 days',
  found: '{n} results in {p} projects',
  openInChat: 'Open in the chat',
  context: 'In the lead’s chat · agentbox',
  searchTitle: 'Search',
  resultsIn: '{n} results in {f} places',
  collapseAll: 'Collapse all',
  replied: 'The lead replied',
  homeChat: 'Home chat',
  agentGone: 'destroyed',
  done: 'done',
  open: 'open',
};
type Strings = typeof en;

const pt: Strings = {
  findA: 'A · Pílula de busca com marcas de resultado',
  findB: 'B · Faixa de busca fixa com lista de resultados',
  findC: 'C · Busca no lugar do campo de mensagem, com linha do tempo',
  modalA: 'Modal A · Uma coluna, agrupada por tipo',
  modalB: 'Modal B · Lista por relevância com prévia',
  modalC: 'Modal C · Filtros, agrupado por onde está',
  modalScoped: 'Modal A, aberto pela aba Memória: já filtrado',
  findThere: 'Buscar naquele chat',
  matchCase: 'Diferenciar maiúsculas',
  wholeWord: 'Palavra inteira',
  from: 'De',
  fromAll: 'Qualquer um',
  fromYou: 'Você',
  fromLead: 'Líder',
  fromAgents: 'Agentes',
  you: 'Você',
  lead: 'Líder',
  notLoaded: '{n} anteriores, ainda não carregados',
  notLoadedHint: 'Ir até um deles o carrega.',
  results: '{n} mensagens',
  hideResults: 'Ocultar lista',
  today: 'Hoje',
  yesterday: 'Ontem',
  earlier: '3 de outubro',
  findInChat: 'Buscar neste chat',
  backToComposer: 'Voltar a escrever',
  conversation: 'Esta conversa',
  started: 'Começou em 1º de out.',
  now: 'Agora',
  searchEverything: 'Buscar em todos os projetos…',
  all: 'Tudo',
  chats: 'Chats',
  reports: 'Relatórios',
  memory: 'Memória',
  media: 'Mídia',
  tasks: 'Tarefas',
  events: 'Eventos',
  anyProject: 'Todos os projetos',
  anyTime: 'Qualquer data',
  anyone: 'Qualquer um',
  openAll: 'ver todos os resultados',
  toFilter: 'para filtrar',
  more: 'mais {n}',
  newKind: 'novo',
  bestFirst: 'Mais relevantes',
  newestFirst: 'Mais recentes',
  kinds: 'O quê',
  projects: 'Projetos',
  agents: 'Agentes',
  when: 'Quando',
  lastWeek: 'Últimos 7 dias',
  lastMonth: 'Últimos 30 dias',
  found: '{n} resultados em {p} projetos',
  openInChat: 'Abrir no chat',
  context: 'No chat do líder · agentbox',
  searchTitle: 'Busca',
  resultsIn: '{n} resultados em {f} lugares',
  collapseAll: 'Recolher tudo',
  replied: 'O líder respondeu',
  homeChat: 'Chat inicial',
  agentGone: 'destruído',
  done: 'concluída',
  open: 'aberta',
};

function useS(): (key: keyof Strings, vars?: Record<string, string | number>) => string {
  const s = useLanguage() === 'pt-BR' ? pt : en;
  return (key, vars) => s[key].replace(/\{(\w+)\}/g, (_, k: string) => String(vars?.[k] ?? ''));
}

// ---------------------------------------------------------------- the lead's chat

// searchChat is a long conversation with the lead about media retention, so
// the word turns up in many messages, some of them days back.
export function searchChat(): T.ChatThread {
  const at = (ago: number) => new Date(Date.now() - ago).toISOString();
  const lines: [string, string, number][] = [
    ['user', 'Media piles up fast. Can we keep only the last two weeks unless I say otherwise?', 8 * DAY],
    ['assistant', 'Yes: a retention sweep that deletes media older than a cut-off. I’ll make the cut-off a setting, two weeks by default, and give it to an agent.', 8 * DAY - 2 * MIN],
    ['user', 'And when an agent is destroyed?', 2 * DAY + 5 * HOUR],
    ['assistant', 'Its media follows the same retention rule: kept until the cut-off, then swept. “Immediately” deletes it with the agent.', 2 * DAY + 5 * HOUR - MIN],
    ['user', 'Some recordings I want forever. Starred ones should never be swept.', 26 * HOUR],
    ['assistant', 'I created agent-116 to add a favourite toggle. A starred item is skipped by the retention sweep and survives its agent’s destroy.', 26 * HOUR - 2 * MIN],
    ['user', 'Did agent-116 finish?', 22 * HOUR],
    ['assistant', 'It did. Starring media is up for review in #229. Its tests pass, and a starred item survives retention “immediately” too.', 22 * HOUR - MIN],
    ['user', 'Merged. Now I want to find things in chats like this one: Ctrl+F here, and something that searches everything.', 3 * HOUR],
    ['assistant', 'I created agent-117 to make two or three mockups of each, in the app’s own styling, before anything is built.', 3 * HOUR - MIN],
    ['user', 'What did we decide about retention for the Home chat’s media?', 40 * MIN],
    ['assistant', 'Nothing yet: the Home chat has no agents, so the retention sweep never reaches what you attach there. Want a rule for it?', 39 * MIN],
  ];
  return {
    agent: LEAD,
    seq: 1,
    older: true,
    session: { state: 'idle', tool: 'claude', options: [], commands: [] },
    items: lines.map(([kind, text, ago], i) => ({
      id: `s${i}`,
      turn: `t${Math.floor(i / 2)}`,
      kind,
      text,
      createdAt: at(ago),
      updatedAt: at(ago),
      ...(kind === 'user'
        ? {
            result: {
              state: 'completed',
              stopReason: 'end_turn',
              endedAt: at(ago - MIN),
            },
          }
        : {}),
    })),
  };
}

// The hits, newest last, as the daemon would answer them: the ones loaded
// (s1…s11) and nine further back that the page hasn't loaded yet.
const loadedHits = ['s1', 's3', 's5', 's7', 's11'];
const CURRENT = 's5';
const TOTAL = 14;
const UNLOADED = TOTAL - loadedHits.length;

// useMarks marks the query in the chat's loaded messages, the way FindBar
// does, and brings the current one into view.
function useMarks(on: boolean, at = 0.5) {
  useEffect(() => {
    if (!on || typeof CSS === 'undefined' || !('highlights' in CSS)) return;
    let tries = 0;
    const timer = setInterval(() => {
      const box = document.querySelector('[data-chat]');
      const here = box?.querySelector(`[data-chat-id="${CURRENT}"]`);
      if (!box || !here) return void (++tries > 50 && clearInterval(timer));
      clearInterval(timer);
      const all: Range[] = [];
      const now: Range[] = [];
      for (const id of loadedHits) {
        const row = box.querySelector(`[data-chat-id="${id}"]`);
        if (row) (id === CURRENT ? now : all).push(...rangesIn(row, QUERY));
      }
      const current = new Highlight(...now);
      current.priority = 1;
      CSS.highlights.set('chat-find', new Highlight(...all));
      CSS.highlights.set('chat-find-current', current);
      // At a fraction of the chat's height, clear of what each option puts over it.
      const scroller = here.closest('.overflow-y-auto');
      if (scroller) scroller.scrollTop += here.getBoundingClientRect().top - scroller.getBoundingClientRect().top - scroller.clientHeight * at;
    }, 100);
    return () => clearInterval(timer);
  }, [on, at]);
}

// InChat renders children inside the chat's own box, where FindBar sits.
function InChat({ children }: { children: ReactNode }) {
  const [box, setBox] = useState<Element | null>(null);
  useEffect(() => {
    const timer = setInterval(() => {
      const found = document.querySelector('[data-chat]');
      if (found) {
        setBox(found);
        clearInterval(timer);
      }
    }, 50);
    return () => clearInterval(timer);
  }, []);
  return box ? createPortal(children, box) : null;
}

// ---------------------------------------------------------------- shared bits

const iconButton = 'flex size-7 items-center justify-center rounded-lg text-subtle transition-colors hover:bg-surface-raised hover:text-title';
const kbd = 'rounded border border-line px-1 font-mono text-[10.5px] text-subtle';

function Mark({ text }: { text: string }) {
  const i = text.toLowerCase().indexOf(QUERY);
  if (i < 0) return <>{text}</>;
  return (
    <>
      {text.slice(0, i)}
      <mark className="rounded-[3px] bg-brand-400/25 text-inherit">{text.slice(i, i + QUERY.length)}</mark>
      {text.slice(i + QUERY.length)}
    </>
  );
}

function Toggle({ label, glyph, on }: { label: string; glyph: string; on?: boolean }) {
  return (
    <button className={cn(iconButton, 'w-auto px-1.5 font-mono text-[11px]', on && 'bg-brand-400/15 text-brand-300')} aria-label={label} title={label} aria-pressed={!!on}>
      {glyph}
    </button>
  );
}

function Chip({ children, on, icon: Icon, badge }: { children: ReactNode; on?: boolean; icon?: LucideIcon; badge?: string }) {
  return (
    <span
      className={cn('inline-flex shrink-0 items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11.5px]', on ? 'border-brand-400/40 bg-brand-400/15 text-brand-200' : 'border-line text-muted')}
    >
      {Icon && <Icon className="size-3.5" />}
      {children}
      {badge && <span className="rounded bg-brand-400/20 px-1 text-[9.5px] font-semibold uppercase tracking-wide text-brand-200">{badge}</span>}
    </span>
  );
}

function Label({ children }: { children: ReactNode }) {
  return (
    <div
      className="pointer-events-none fixed bottom-3 left-1/2 z-[60] -translate-x-1/2 rounded-full border border-line-strong bg-overlay px-3 py-1 text-[11.5px] font-medium text-title shadow-lg backdrop-blur-xl"
      data-search-mock-label
    >
      {children}
    </div>
  );
}

// ---------------------------------------------------------------- Ctrl+F

// The count and arrows every find option shares.
function Stepper({ wide }: { wide?: boolean }) {
  const t = useT();
  return (
    <>
      <span className={cn('flex items-center justify-end px-1 text-[12px] tabular-nums text-subtle', wide ? 'min-w-20' : 'min-w-14')}>{t('chat.find.count', { current: 8, total: TOTAL })}</span>
      <button className={iconButton} aria-label={t('chat.find.older')}>
        <ChevronUp className="size-4" />
      </button>
      <button className={iconButton} aria-label={t('chat.find.newer')}>
        <ChevronDown className="size-4" />
      </button>
    </>
  );
}

// Find A: the pill as it ships, with match case and whole word, and ticks on
// the scroll track for every match: loaded ones where they are, the ones not
// loaded yet stacked at the top as one hatched mark with their count.
function FindA() {
  const t = useT();
  const s = useS();
  const ticks = [0.18, 0.34, 0.52, 0.66, 0.9];
  return (
    <InChat>
      <div className="absolute right-3 top-3 z-30 flex items-center gap-1 rounded-xl border border-line-strong bg-overlay py-1 pl-2.5 pr-1 shadow-lg backdrop-blur-xl md:right-5" role="search">
        <Search className="size-3.5 shrink-0 text-subtle" />
        <input readOnly value={QUERY} aria-label={t('chat.find.label')} className="h-7 w-44 min-w-0 bg-transparent text-[13px] text-primary focus:outline-none sm:w-52" />
        <Toggle label={s('matchCase')} glyph="Aa" />
        <Toggle label={s('wholeWord')} glyph="ab" on />
        <span className="mx-0.5 h-4 w-px bg-line" />
        <Stepper />
        <button className={cn(iconButton, 'ml-0.5')} aria-label={t('chat.find.close')}>
          <X className="size-4" />
        </button>
      </div>
      {/* The scroll track's marks. */}
      <div className="pointer-events-none absolute bottom-36 right-0.5 top-2 z-20 w-2">
        <div className="absolute inset-x-0 top-0 h-5 rounded-sm bg-[repeating-linear-gradient(45deg,#f59e0b_0_2px,transparent_2px_4px)] opacity-70" title={s('notLoaded', { n: UNLOADED })} />
        {ticks.map((y, i) => (
          <div key={y} className={cn('absolute inset-x-0 h-[3px] rounded-full', i === 2 ? 'bg-amber-500' : 'bg-yellow-400/60')} style={{ top: `${8 + y * 92}%` }} />
        ))}
      </div>
      <div className="absolute right-4 top-14 z-30 rounded-lg border border-line bg-overlay px-2.5 py-1.5 text-[11.5px] text-muted shadow-lg backdrop-blur-xl md:right-6">
        <span className="mr-1.5 inline-block size-2 rounded-sm bg-[repeating-linear-gradient(45deg,#f59e0b_0_2px,transparent_2px_4px)] align-middle" />
        {s('notLoaded', { n: UNLOADED })} · <span className="text-subtle">{s('notLoadedHint')}</span>
      </div>
    </InChat>
  );
}

// The messages Find B lists and the timeline of Find C marks.
function useHitList(): {
  day: string;
  rows: {
    who: string;
    time: string;
    text: string;
    current?: boolean;
    unloaded?: boolean;
  }[];
}[] {
  const s = useS();
  return [
    {
      day: s('today'),
      rows: [
        {
          who: s('lead'),
          time: '14:21',
          text: 'Nothing yet: the Home chat has no agents, so the retention sweep never reaches what you attach there.',
        },
        {
          who: s('you'),
          time: '14:20',
          text: 'What did we decide about retention for the Home chat’s media?',
        },
      ],
    },
    {
      day: s('yesterday'),
      rows: [
        {
          who: s('lead'),
          time: '16:58',
          text: 'It did. Starring media is up for review in #229. Its tests pass, and a starred item survives retention “immediately” too.',
        },
        {
          who: s('lead'),
          time: '12:47',
          text: 'I created agent-116 to add a favourite toggle. A starred item is skipped by the retention sweep…',
          current: true,
        },
      ],
    },
    {
      day: s('earlier'),
      rows: [
        {
          who: s('lead'),
          time: '09:12',
          text: '…the retention cut-off is now a setting, two weeks by default, on Settings → Media.',
          unloaded: true,
        },
        {
          who: s('you'),
          time: '09:05',
          text: 'Where do I change the retention period?',
          unloaded: true,
        },
      ],
    },
  ];
}

// Find B: a strip across the top of the chat, the list of matching messages
// under it, which you can read and pick from rather than step through.
function FindB() {
  const t = useT();
  const s = useS();
  const groups = useHitList();
  return (
    <InChat>
      <div className="absolute inset-x-0 top-0 z-30 border-b border-line bg-overlay shadow-lg backdrop-blur-xl" role="search">
        <div className="mx-auto flex w-full max-w-4xl items-center gap-1.5 px-4 py-1.5 md:px-6">
          <Search className="size-4 shrink-0 text-subtle" />
          <input readOnly value={QUERY} aria-label={t('chat.find.label')} className="h-8 min-w-0 flex-1 bg-transparent text-[13.5px] text-primary focus:outline-none" />
          <Toggle label={s('matchCase')} glyph="Aa" />
          <Toggle label={s('wholeWord')} glyph="ab" on />
          <span className="flex items-center gap-1 rounded-lg border border-line px-2 py-1 text-[11.5px] text-muted">
            {s('from')}: {s('fromAll')} <ChevronDown className="size-3" />
          </span>
          <span className="mx-1 h-4 w-px bg-line" />
          <Stepper wide />
          <button className={cn(iconButton, 'ml-0.5')} aria-label={t('chat.find.close')}>
            <X className="size-4" />
          </button>
        </div>
        <div className="mx-auto w-full max-w-4xl px-4 pb-2 md:px-6">
          <div className="max-h-[300px] overflow-hidden rounded-xl border border-line bg-modal">
            <div className="flex items-center justify-between border-b border-line px-3 py-1.5 text-[11px] text-subtle">
              <span>{s('results', { n: TOTAL })}</span>
              <span className="flex items-center gap-1">
                {s('hideResults')} <ChevronUp className="size-3" />
              </span>
            </div>
            {groups.map((g) => (
              <div key={g.day}>
                <h3 className="px-3 pb-0.5 pt-2 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle">{g.day}</h3>
                {g.rows.map((r) => (
                  <div key={r.time + r.text} className={cn('mx-1 flex items-start gap-3 rounded-lg px-2 py-1.5 text-[12.5px]', r.current && 'bg-surface-raised')}>
                    <span className={cn('w-12 shrink-0 text-[11.5px]', r.who === s('you') ? 'text-brand-300' : 'text-subtle')}>{r.who}</span>
                    <span className={cn('min-w-0 flex-1 truncate', r.unloaded ? 'text-muted' : 'text-primary')}>
                      <Mark text={r.text} />
                    </span>
                    <span className="shrink-0 text-[11px] tabular-nums text-faint">{r.time}</span>
                    {r.current && <CornerDownLeft className="mt-0.5 size-3.5 shrink-0 text-subtle" />}
                  </div>
                ))}
              </div>
            ))}
          </div>
        </div>
      </div>
    </InChat>
  );
}

// Find C: the composer becomes the find bar, where the eyes already are, and
// a strip above it lays the whole conversation out in time with its matches.
function FindC() {
  const t = useT();
  const s = useS();
  const marks = [0.04, 0.09, 0.15, 0.22, 0.31, 0.38, 0.47, 0.55, 0.61, 0.72, 0.8, 0.86, 0.95, 0.98];
  return (
    <InChat>
      <div className="absolute inset-x-0 bottom-0 z-30 bg-chat pb-4 pt-2">
        <div className="mx-auto w-full max-w-4xl px-4 md:px-6">
          <div className="rounded-2xl border border-line-strong bg-surface shadow-lg">
            <div className="border-b border-line px-4 pb-2 pt-2.5">
              <div className="flex items-center justify-between text-[10.5px] text-subtle">
                <span>
                  {s('conversation')} · {s('started')}
                </span>
                <span>{s('now')}</span>
              </div>
              <div className="relative mt-1.5 h-5 rounded-md bg-surface-faint">
                <div className="absolute inset-y-0 right-0 w-[34%] rounded-r-md border-l border-line bg-surface-raised/60" title="loaded" />
                {marks.map((x, i) => (
                  <div key={x} className={cn('absolute top-1 h-3 w-[3px] rounded-full', i === 10 ? 'bg-amber-500 ring-2 ring-amber-500/30' : 'bg-yellow-400/70')} style={{ left: `${x * 100}%` }} />
                ))}
              </div>
            </div>
            <div className="flex items-center gap-1.5 px-3 py-2">
              <Search className="size-4 shrink-0 text-subtle" />
              <input readOnly value={QUERY} aria-label={t('chat.find.label')} className="h-9 min-w-0 flex-1 bg-transparent text-[14px] text-primary focus:outline-none" />
              <Chip on>{s('fromAll')}</Chip>
              <Chip>{s('fromYou')}</Chip>
              <Chip>{s('fromLead')}</Chip>
              <Chip>{s('fromAgents')}</Chip>
              <span className="mx-1 h-4 w-px bg-line" />
              <Stepper wide />
            </div>
            <div className="flex items-center justify-between border-t border-line px-4 py-1.5 text-[11px] text-subtle">
              <span className="flex items-center gap-3">
                <span className="flex items-center gap-1.5">
                  <kbd className={kbd}>↵</kbd>/<kbd className={kbd}>⇧↵</kbd> {t('chat.find.older').replace(/ \(.*\)$/, '')} / {t('chat.find.newer').replace(/ \(.*\)$/, '')}
                </span>
              </span>
              <span className="flex items-center gap-1.5">
                <kbd className={kbd}>Esc</kbd> {s('backToComposer')}
              </span>
            </div>
          </div>
        </div>
      </div>
    </InChat>
  );
}

// ---------------------------------------------------------------- Ctrl+Shift+F

type Hit = {
  icon: LucideIcon;
  title: string;
  detail?: string;
  tag?: string;
  where: string;
  when: string;
};
type Group = {
  kind: string;
  icon: LucideIcon;
  count: number;
  hits: Hit[];
  isNew?: boolean;
};

function useGroups(): Group[] {
  const s = useS();
  return [
    {
      kind: s('chats'),
      icon: MessageSquare,
      count: 23,
      hits: [
        {
          icon: MessageSquare,
          title: 'Nothing yet: the Home chat has no agents, so the retention sweep never reaches…',
          tag: 'assistant',
          where: `agentbox · ${s('lead')}`,
          when: '40m',
        },
        {
          icon: MessageSquare,
          title: 'A favorite is skipped by the retention sweep and survives its agent’s destroy',
          tag: 'assistant',
          where: 'agentbox · agent-116',
          when: '1d',
        },
        {
          icon: MessageSquare,
          title: 'Keep the order history for 7 years, retention rules from the accountant',
          tag: 'user',
          where: `organic · ${s('lead')}`,
          when: '6d',
        },
      ],
    },
    {
      kind: s('reports'),
      icon: FileText,
      count: 3,
      hits: [
        {
          icon: FileText,
          title: 'Favorite toggle on media: never auto-removed, Favorites filter in the all-media view',
          detail: 'A favorite is skipped by the retention sweep and survives its agent’s destroy, both with retention “immediately”…',
          tag: 'done',
          where: 'agentbox · agent-116',
          when: '3d',
        },
      ],
    },
    {
      kind: s('memory'),
      icon: Brain,
      count: 4,
      hits: [
        {
          icon: Brain,
          title: 'Starred media is exempt from retention',
          detail: 'Decided with #229: the sweep never deletes a favourite, nor does destroying its agent.',
          tag: 'decision',
          where: 'agentbox',
          when: '3d',
        },
      ],
    },
    {
      kind: s('tasks'),
      icon: ListChecks,
      count: 2,
      isNew: true,
      hits: [
        {
          icon: ListChecks,
          title: 'Media retention for the Home chat’s attachments',
          tag: 'open',
          where: 'agentbox',
          when: '35m',
        },
      ],
    },
    {
      kind: s('media'),
      icon: Image,
      count: 2,
      hits: [
        {
          icon: Image,
          title: 'favorite-media-all-cards',
          detail: 'A starred item still there after the retention sweep ran',
          tag: 'recording',
          where: 'agentbox · agent-116',
          when: '3d',
        },
      ],
    },
  ];
}

function HitRow({ hit, active }: { hit: Hit; active?: boolean }) {
  return (
    <div className={cn('flex w-full items-start gap-3 rounded-xl px-3 py-2 text-left', active ? 'bg-surface-raised' : '')}>
      <hit.icon className={cn('mt-0.5 size-4 shrink-0', active ? 'text-brand-300' : 'text-subtle')} />
      <span className="grid min-w-0 flex-1 gap-0.5">
        <span className="flex min-w-0 items-baseline gap-2">
          <span className="truncate text-[13px] text-primary">
            <Mark text={hit.title} />
          </span>
          {hit.tag && <span className="max-w-[40%] shrink-0 truncate font-mono text-[10.5px] text-subtle">{hit.tag}</span>}
        </span>
        {hit.detail && (
          <span className="line-clamp-2 break-words text-[12px] leading-snug text-muted">
            <Mark text={hit.detail} />
          </span>
        )}
      </span>
      <span className="flex max-w-[34%] shrink-0 flex-col items-end gap-0.5 pt-0.5 text-[11px] text-subtle">
        <span className="max-w-full truncate">{hit.where}</span>
        <span className="text-faint">{hit.when}</span>
      </span>
      {active && <CornerDownLeft className="mt-0.5 size-3.5 shrink-0 text-subtle" />}
    </div>
  );
}

// Global A: the palette as it ships (SearchPalette), with chips that narrow
// it to a kind, a project, a time, and a way into all of a group's results.
// SearchModal is the one modal every option shares, as it would be built: a
// dialog over the app's dimmed, blurred backdrop (the scrim SearchPalette and
// the media viewer use), its width the layout's, opened by Ctrl+Shift+F or by
// any place in the app that wants a search, with a scope already set (the
// project's Memory tab opening it on that project's memory, say).
function SearchModal({ wide, scope, children }: { wide: 'md' | 'lg' | 'xl'; scope?: ReactNode; children: ReactNode }) {
  const t = useT();
  return (
    <>
      <div className="fixed inset-0 z-50 bg-scrim backdrop-blur-[3px]" />
      <div
        role="dialog"
        aria-label={t('search.title')}
        className={cn(
          'fixed left-1/2 top-[8vh] z-50 flex h-[78vh] w-[calc(100vw-2rem)] -translate-x-1/2 flex-col overflow-hidden rounded-2xl border border-line-strong bg-modal shadow-[0_40px_120px_-30px_var(--ab-shadow-deep)]',
          wide === 'md' ? 'max-w-2xl' : wide === 'lg' ? 'max-w-4xl' : 'max-w-6xl',
        )}
      >
        <div className="flex items-center gap-3 border-b border-line px-4">
          <Search className="size-4 shrink-0 text-subtle" />
          {scope}
          <input readOnly value={QUERY} aria-label={t('search.label')} className="h-14 min-w-0 flex-1 bg-transparent text-[15px] text-primary outline-none" />
          <kbd className="rounded-md border border-line px-1.5 py-0.5 font-mono text-[10.5px] text-subtle">Esc</kbd>
        </div>
        {children}
      </div>
    </>
  );
}

function Keys({ extra }: { extra?: ReactNode }) {
  const t = useT();
  return (
    <div className="flex shrink-0 items-center gap-4 border-t border-line px-4 py-2 text-[11px] text-subtle">
      <span className="flex items-center gap-1.5">
        <kbd className={kbd}>↑</kbd>
        <kbd className={kbd}>↓</kbd>
        {t('search.keys.move')}
      </span>
      <span className="flex items-center gap-1.5">
        <kbd className={kbd}>↵</kbd>
        {t('search.keys.open')}
      </span>
      {extra}
    </div>
  );
}

// The filters along the top of A and B: a chip per kind, and the project and
// time as menus.
function KindChips({ counts, scoped }: { counts?: boolean; scoped?: boolean }) {
  const s = useS();
  const n = (k: number) => (counts ? ` ${k}` : '');
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-1.5 border-b border-line px-3 py-2">
      <Chip on={!scoped}>
        {s('all')}
        {n(34)}
      </Chip>
      <Chip icon={MessageSquare}>
        {s('chats')}
        {n(23)}
      </Chip>
      <Chip icon={FileText}>
        {s('reports')}
        {n(3)}
      </Chip>
      <Chip icon={Brain} on={scoped}>
        {s('memory')}
        {n(4)}
      </Chip>
      <Chip icon={ListChecks} badge={s('newKind')}>
        {s('tasks')}
        {n(2)}
      </Chip>
      <Chip icon={Image}>
        {s('media')}
        {n(2)}
      </Chip>
      <span className="flex-1" />
      <Chip icon={FolderGit2} on={scoped}>
        {scoped ? 'agentbox' : s('anyProject')} <ChevronDown className="size-3" />
      </Chip>
      <Chip icon={CalendarDays}>
        {s('anyTime')} <ChevronDown className="size-3" />
      </Chip>
    </div>
  );
}

// Modal A: one column, results grouped by kind, best few of each, "N more"
// narrowing to that kind. The palette as it ships, with filters.
function ModalA({ scoped }: { scoped?: boolean }) {
  const s = useS();
  const groups = useGroups();
  const shown = scoped ? groups.filter((g) => g.kind === s('memory')) : groups;
  return (
    <SearchModal
      wide="md"
      scope={
        scoped && (
          <span className="flex shrink-0 items-center gap-1 rounded-md bg-brand-400/15 px-2 py-0.5 text-[12px] text-brand-200">
            agentbox · {s('memory')} <X className="size-3" />
          </span>
        )
      }
    >
      <KindChips scoped={scoped} />
      <div className="min-h-0 flex-1 overflow-y-auto p-2">
        {shown.map((g, gi) => {
          const hits = scoped ? memoryHits : g.hits;
          return (
            <div key={g.kind} className="pb-2">
              <h3 className="flex items-center gap-2 px-3 pb-1 pt-2 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle">
                {g.kind}
                {g.count > hits.length && (
                  <span className="flex items-center gap-0.5 font-normal normal-case tracking-normal text-brand-300">
                    {s('more', { n: g.count - hits.length })} <ChevronRight className="size-3" />
                  </span>
                )}
              </h3>
              {hits.map((h, i) => (
                <HitRow key={h.title} hit={h} active={gi === 0 && i === 0} />
              ))}
            </div>
          );
        })}
      </div>
      <Keys
        extra={
          <span className="flex items-center gap-1.5">
            <kbd className={kbd}>Tab</kbd>
            {s('toFilter')}
          </span>
        }
      />
    </SearchModal>
  );
}

const memoryHits: Hit[] = [
  {
    icon: Brain,
    title: 'Starred media is exempt from retention',
    detail: 'Decided with #229: the sweep never deletes a favourite, nor does destroying its agent.',
    tag: 'decision',
    where: 'agentbox',
    when: '3d',
  },
  {
    icon: Brain,
    title: 'Removed agents’ media: retention is 1 day by default',
    detail: '`agentbox media retention` changes it; “immediately” deletes with the agent.',
    tag: 'fact',
    where: 'agentbox',
    when: '9d',
  },
  {
    icon: Brain,
    title: 'Home chat attachments have no retention rule yet',
    tag: 'open',
    where: 'agentbox',
    when: '35m',
  },
];

// The hit in context: the messages around it, the match marked, and the ways
// out of the modal to where it lives.
function Preview() {
  const s = useS();
  return (
    <aside className="flex w-[42%] min-w-0 shrink-0 flex-col border-l border-line bg-surface-faint">
      <div className="flex items-center justify-between gap-2 border-b border-line px-4 py-2.5 text-[12px] text-subtle">
        <span className="flex min-w-0 items-center gap-1.5 truncate">
          <Bot className="size-3.5 shrink-0" /> agentbox · agent-116
        </span>
        <span className="text-faint">{s('yesterday')} 12:47</span>
      </div>
      <div className="grid min-h-0 flex-1 content-start gap-3 overflow-y-auto px-4 py-4 text-[13px] leading-relaxed">
        <div className="text-muted">The sweep reads the media table, oldest first, and stops at the cut-off.</div>
        <div className="ml-auto max-w-[85%] rounded-2xl bg-surface-raised px-3 py-2 text-primary">Make sure a favourite never disappears on its own.</div>
        <div className="rounded-xl px-3 py-2 text-primary ring-1 ring-amber-500/50">
          Done. A favorite is skipped by the <mark className="rounded-[3px] bg-amber-500 text-stone-900">retention</mark> sweep and survives its agent’s destroy, with{' '}
          <mark className="rounded-[3px] bg-yellow-400/30 text-inherit">retention</mark> “immediately” and with DeleteMedia asked for.
        </div>
        <div className="text-muted">Tests pass: TestSweepSkipsFavorites, TestDestroyKeepsFavorites.</div>
      </div>
      <div className="flex shrink-0 items-center gap-2 border-t border-line px-4 py-2.5">
        <span className="flex items-center gap-1.5 rounded-lg bg-brand-500 px-2.5 py-1 text-[12px] font-medium text-white">
          {s('openInChat')} <kbd className="font-mono text-[10.5px] opacity-80">↵</kbd>
        </span>
        <span className="flex items-center gap-1.5 rounded-lg border border-line px-2.5 py-1 text-[12px] text-muted">
          {s('findThere')} <kbd className="font-mono text-[10.5px] text-subtle">⇧↵</kbd>
        </span>
      </div>
    </aside>
  );
}

// Modal B: wide, the results in one list ranked across kinds, and the picked
// one in context beside it, so a chat message can be read before leaving.
function ModalB() {
  const s = useS();
  const groups = useGroups();
  const hits = [
    groups[0].hits[1],
    groups[1].hits[0],
    groups[0].hits[0],
    groups[2].hits[0],
    {
      icon: MessageSquare,
      title: 'Its media follows the same retention rule: kept until the cut-off, then swept.',
      tag: 'assistant',
      where: `agentbox · ${s('lead')}`,
      when: '2d',
    },
    groups[3].hits[0],
    groups[0].hits[2],
    groups[4].hits[0],
  ];
  return (
    <SearchModal wide="xl">
      <KindChips counts />
      <div className="flex min-h-0 flex-1">
        <div className="min-w-0 flex-1 overflow-y-auto p-2">
          <div className="flex items-center justify-between px-3 pb-1 pt-1 text-[11px] text-subtle">
            <span>{s('found', { n: 34, p: 2 })}</span>
            <span className="flex items-center gap-1">
              <ArrowDownUp className="size-3" /> {s('bestFirst')} <ChevronDown className="size-3" />
            </span>
          </div>
          {hits.map((h, i) => (
            <HitRow key={h.title} hit={h} active={i === 0} />
          ))}
        </div>
        <Preview />
      </div>
      <Keys />
    </SearchModal>
  );
}

function Facet({ title, rows }: { title: string; rows: [string, number, boolean?][] }) {
  return (
    <div className="grid gap-0.5">
      <h3 className="px-2 pb-1 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle">{title}</h3>
      {rows.map(([label, n, on]) => (
        <div key={label} className={cn('flex items-center gap-2 rounded-lg px-2 py-1 text-[12.5px]', on ? 'bg-surface-raised text-title' : 'text-muted')}>
          <span className={cn('size-3.5 shrink-0 rounded border', on ? 'border-brand-400 bg-brand-400' : 'border-line-strong')} />
          <span className="min-w-0 flex-1 truncate">{label}</span>
          <span className="text-[11px] tabular-nums text-faint">{n}</span>
        </div>
      ))}
    </div>
  );
}

// Modal C: facets down the left with their counts, the results grouped by
// where they live (project, then chat, agent, reports…), each place's matches
// under it, so a chat with nine matches is one entry, not nine.
function ModalC() {
  const s = useS();
  const places: {
    project: string;
    name: string;
    icon: LucideIcon;
    count: number;
    lines: Hit[];
  }[] = [
    {
      project: 'agentbox',
      name: s('lead'),
      icon: MessageSquare,
      count: 5,
      lines: [
        {
          icon: MessageSquare,
          title: '…so the retention sweep never reaches what you attach there.',
          where: '',
          when: '40m',
        },
        {
          icon: MessageSquare,
          title: '…a starred item survives retention “immediately” too.',
          where: '',
          when: '22h',
        },
      ],
    },
    {
      project: 'agentbox',
      name: 'agent-116 · Favorite toggle on media',
      icon: Bot,
      count: 9,
      lines: [
        {
          icon: MessageSquare,
          title: 'A favorite is skipped by the retention sweep and survives its agent’s destroy',
          where: '',
          when: '1d',
        },
        {
          icon: FileText,
          title: 'Report: …skipped by the retention sweep, both with retention “immediately”…',
          where: '',
          when: '3d',
        },
      ],
    },
    {
      project: 'agentbox',
      name: s('memory'),
      icon: Brain,
      count: 4,
      lines: [
        {
          icon: Brain,
          title: 'Starred media is exempt from retention',
          where: '',
          when: '3d',
        },
      ],
    },
    {
      project: 'organic',
      name: s('lead'),
      icon: MessageSquare,
      count: 5,
      lines: [
        {
          icon: MessageSquare,
          title: 'Keep the order history for 7 years, retention rules from the accountant',
          where: '',
          when: '6d',
        },
      ],
    },
  ];
  return (
    <SearchModal wide="lg">
      <div className="flex min-h-0 flex-1">
        <aside className="grid w-52 shrink-0 content-start gap-5 overflow-y-auto border-r border-line px-2.5 py-3">
          <Facet
            title={s('kinds')}
            rows={[
              [s('chats'), 23, true],
              [s('reports'), 3, true],
              [s('memory'), 4, true],
              [s('tasks'), 2, true],
              [s('media'), 2, true],
              [s('events'), 9],
            ]}
          />
          <Facet
            title={s('projects')}
            rows={[
              ['agentbox', 27, true],
              ['organic', 7, true],
            ]}
          />
          <Facet
            title={s('when')}
            rows={[
              [s('anyTime'), 34, true],
              [s('lastWeek'), 30],
              [s('lastMonth'), 34],
            ]}
          />
        </aside>
        <div className="min-w-0 flex-1 overflow-y-auto p-2">
          {places.map((p, pi) => (
            <div key={p.project + p.name} className="pb-1.5">
              <div className="flex items-center gap-2 px-3 pb-1 pt-2 text-[12px]">
                <ChevronDown className="size-3.5 text-subtle" />
                <p.icon className="size-3.5 text-subtle" />
                <span className="min-w-0 truncate font-medium text-title">{p.name}</span>
                <span className="shrink-0 text-subtle">· {p.project}</span>
                <span className="ml-auto shrink-0 rounded-full bg-surface-raised px-1.5 text-[10.5px] tabular-nums text-subtle">{p.count}</span>
              </div>
              {p.lines.map((l, i) => (
                <div key={l.title} className={cn('ml-8 mr-1 flex items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-[12.5px]', pi === 0 && i === 0 ? 'bg-surface-raised text-primary' : 'text-muted')}>
                  <l.icon className="size-3.5 shrink-0 text-subtle" />
                  <span className="min-w-0 flex-1 truncate">
                    <Mark text={l.title} />
                  </span>
                  <span className="shrink-0 text-[11px] text-faint">{l.when}</span>
                  {pi === 0 && i === 0 && <CornerDownLeft className="size-3.5 shrink-0 text-subtle" />}
                </div>
              ))}
              {p.count > p.lines.length && <div className="ml-10 px-2.5 py-0.5 text-[11.5px] text-brand-300">{s('more', { n: p.count - p.lines.length })}</div>}
            </div>
          ))}
        </div>
      </div>
      <Keys />
    </SearchModal>
  );
}

// ---------------------------------------------------------------- the shell

const labels: Record<string, keyof Strings> = {
  'find-a': 'findA',
  'find-b': 'findB',
  'find-c': 'findC',
  'modal-a': 'modalA',
  'modal-b': 'modalB',
  'modal-c': 'modalC',
  'modal-scoped': 'modalScoped',
};

export function SearchPreview({ scenario }: { scenario: string }) {
  const s = useS();
  const queryClient = useQueryClient();
  // Before the chat mounts, so it reads this conversation.
  useState(() => {
    queryClient.setQueryData(['chat', LEAD], searchChat());
    queryClient.setQueryData(['claudeLimits'], []);
    // What the Memory tab behind the scoped modal reads, which the dev bridge
    // doesn't answer.
    const at = new Date().toISOString();
    queryClient.setQueryData(
      ['memories', PROJECT, ''],
      memoryHits.map((h, i): T.Memory => ({
        id: `m${i}`,
        project: PROJECT,
        origin: 'lead',
        kind: h.tag ?? 'fact',
        title: h.title,
        content: h.detail ?? '',
        importance: 3,
        createdAt: at,
        updatedAt: at,
      })),
    );
    queryClient.setQueryData(['memoryWorking', PROJECT], {
      goal: 'Find things in chats',
      currentTask: '',
      blockers: [],
      notes: '',
      activeAgents: [],
    } satisfies T.WorkingMemory);
    queryClient.setQueryData(['memoryEvents', PROJECT, 50], []);
    queryClient.setQueryData(['memoryReports', PROJECT], []);
    queryClient.setQueryData(['memoryArtifacts', PROJECT], []);
  });
  const finding = scenario.startsWith('find');
  useMarks(finding, scenario === 'find-b' ? 0.62 : scenario === 'find-c' ? 0.25 : 0.4);
  const view = { kind: 'project', project: PROJECT } as const;
  return (
    <div className="flex h-screen w-screen" style={{ background: 'var(--color-ink)', font: '13px var(--font-sans)' }} data-preview-search={scenario}>
      <Sidebar view={view} onSelect={() => {}} onAddProject={() => {}} onNewAgent={() => {}} />
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar view={view} onSelect={() => {}} onOpenNav={() => {}} onNewAgent={() => {}} />
        <div className="flex min-h-0 flex-1">
          <main className="min-h-0 min-w-0 flex-1">
            <Suspense>
              <ProjectView name={PROJECT} tab={scenario === 'modal-scoped' ? 'memory' : 'chat'} onSelect={() => {}} onNewAgent={() => {}} />
            </Suspense>
          </main>
          <div className="hidden lg:flex">
            <AgentRail view={view} onSelect={() => {}} onNewAgent={() => {}} />
          </div>
        </div>
      </div>
      {scenario === 'find-a' && <FindA />}
      {scenario === 'find-b' && <FindB />}
      {scenario === 'find-c' && <FindC />}
      {scenario === 'modal-a' && <ModalA />}
      {scenario === 'modal-b' && <ModalB />}
      {scenario === 'modal-c' && <ModalC />}
      {scenario === 'modal-scoped' && <ModalA scoped />}
      {labels[scenario] && <Label>{s(labels[scenario])}</Label>}
    </div>
  );
}
