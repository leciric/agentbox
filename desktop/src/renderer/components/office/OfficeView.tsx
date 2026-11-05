// The Office: a 3D room with a desk for every agent of every project, where
// you can walk about and watch them work (office/scene.ts draws it). This is
// its data, read from the queries the rest of the app keeps live, and what's
// over the canvas: a top bar, a tray of who needs you, the desk you're at
// and its chat in place, and the controls. It covers the whole window.
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, ArrowRight, ChevronDown, ChevronRight, ChevronUp, ClipboardList, Flag, GitPullRequest, Keyboard, LoaderCircle, Maximize2, MessageSquare, Scan, X } from 'lucide-react';
import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { agentChatStatus } from '../../lib/chatActivity';
import { chatKey, lastLineOf, peekThread } from '../../lib/chat';
import { useT } from '../../lib/i18n';
import { useReviewHead } from '../../lib/inboxPages';
import { useReviewing } from '../../lib/reviewAlert';
import { useAgentCounts } from '../../lib/useAgentCounts';
import { cn } from '../../lib/utils';
import { floorsOf } from '../../office/layout';
import { deskOf, leadRef, officeModel, plain, readable, type BoardItem, type DeskModel, type DeskTone, type OfficeModel, type PrLook } from '../../office/model';
import type { OfficeScene } from '../../office/scene';
import type { BoardScreen, Screen } from '../../office/textures';
import { ChatTab } from '../chat/ChatTab';
import { ProjectChatPanel } from '../ProjectChatPanel';
import { PullRequestModal } from '../PullRequestModal';
import { Button } from '../ui/button';
import { useOpenReview } from '../v2/ReviewV2';

const hintKey = 'agentbox.office.hintSeen';

// The Office's scene, kept between visits (see the effect that makes it).
let kept: OfficeScene | null = null;

const toneDot: Record<DeskTone, string> = {
  needs: 'bg-amber-400',
  error: 'bg-red-400',
  finished: 'bg-emerald-400',
  working: 'bg-sky-400',
  idle: 'bg-slate-300',
  stopped: 'bg-slate-500',
};

function useReducedMotion(): boolean {
  const [on, setOn] = useState(() => typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches);
  useEffect(() => {
    if (typeof matchMedia !== 'function') return;
    const m = matchMedia('(prefers-reduced-motion: reduce)');
    const on = () => setOn(m.matches);
    m.addEventListener('change', on);
    return () => m.removeEventListener('change', on);
  }, []);
  return on;
}

// useOffice reads what the Office shows: the same queries the sidebar and the
// panels read, so it says what they say and moves when they do.
function useOffice(): { model: OfficeModel; loaded: boolean; agents: ReadonlyMap<string, T.Agent>; review: readonly T.ReviewItem[]; fleets: ReadonlyMap<string, T.Fleet | undefined> } {
  const counts = useAgentCounts();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const names = (projects.data ?? []).map((p) => p.name);
  const fleets = useQueries({ queries: names.map((name) => ({ queryKey: ['fleet', name], queryFn: () => api.fleet(name), refetchInterval: 30_000 })) });
  const questions = useQueries({ queries: names.map((name) => ({ queryKey: ['questions', name], queryFn: () => api.questions(name) })) });
  const chats = useQueries({ queries: names.map((name) => ({ queryKey: ['projectChat', name], queryFn: () => api.projectChat(name), refetchInterval: 60_000 })) });
  const reviewing = useReviewing();
  const head = useReviewHead(reviewing);
  const review = useMemo(() => (reviewing ? (head.data?.items ?? []) : []), [reviewing, head.data]);
  // Keys of what changed, so the maps are made again only when a query's
  // data does, not on every render.
  const namesKey = names.join('\n');
  const fleetKey = fleets.map((f) => f.dataUpdatedAt).join(',');
  const qKey = questions.map((q) => q.dataUpdatedAt).join(',');
  const chatKeyAll = chats.map((c) => `${c.dataUpdatedAt}:${c.data?.chat ?? ''}:${c.data?.last?.at ?? ''}`).join(',');
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const fleetMap = useMemo(() => new Map(names.map((n, i) => [n, fleets[i]?.data])), [namesKey, fleetKey]);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const qMap = useMemo(() => new Map(names.map((n, i) => [n, Array.isArray(questions[i]?.data) ? questions[i].data : undefined])), [namesKey, qKey]);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const leadMap = useMemo(() => new Map(names.map((n, i) => [n, chats[i]?.data])), [namesKey, chatKeyAll]);
  const model = useMemo(
    () => officeModel({ projects: projects.data ?? [], rows: counts.rows, fleets: fleetMap, questions: qMap, leads: leadMap, review }),
    [projects.data, counts.rows, fleetMap, qMap, leadMap, review],
  );
  const agents = useMemo(() => new Map(counts.rows.map((r) => [r.agent.ref, r.agent])), [counts.rows]);
  return { model, loaded: counts.loaded && !!projects.data, agents, review, fleets: fleetMap };
}

// modelOf is the model a chat runs on, as its menu names it.
function modelOf(thread: T.ChatThread | undefined): string | undefined {
  const opt = thread?.session.options.find((o) => o.category === 'model' && o.type === 'select');
  if (!opt?.value || opt.value === 'default') return undefined;
  return opt.choices.find((c) => c.value === opt.value)?.name ?? opt.value;
}

function lastMessage(thread: T.ChatThread | undefined): string | undefined {
  const items = thread?.items ?? [];
  for (let i = items.length - 1; i >= 0; i--) {
    const line = lastLineOf(items[i]);
    if (line && line.kind === 'assistant') return line.text;
  }
  return undefined;
}

// The legend's entries: the bubbles' glyphs and colours (scene.ts's bubbleOf).
const LEGEND = [
  ['!', '#f59e0b', 'office.legend.needs'],
  ['', '#60a5fa', 'office.legend.working'],
  ['✓', '#10b981', 'office.legend.finished'],
  ['×', '#ef4444', 'office.legend.error'],
  ['z', '#94a3b8', 'office.legend.stopped'],
] as const;

export function OfficeView({ onSelect, onBack }: { onSelect: (view: View) => void; onBack: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const host = useRef<HTMLDivElement>(null);
  const scene = useRef<OfficeScene | null>(null);
  const [status, setStatus] = useState<'loading' | 'ready' | 'unsupported'>('loading');
  const [near, setNear] = useState<string | undefined>();
  // Which room the camera's in (none: the campus), and whether at its overview.
  const [view, setView] = useState<{ project?: string; floor: number; floors: number; overview: boolean; refs?: string[] }>({ floor: 1, floors: 1, overview: true });
  // A list folds as you go to another room or out to them all.
  const viewKey = `${view.project ?? ''}#${view.floor}`;
  const lastViewKey = useRef(viewKey);
  const [hover, setHover] = useState<string | undefined>();
  const [hint, setHint] = useState(() => {
    try {
      return localStorage.getItem(hintKey) !== '1';
    } catch {
      return true;
    }
  });
  const [viewingPr, setViewingPr] = useState<{ project: string; pr: T.PullRequest } | null>(null);
  // The chat open in place, over the room: an agent's or a project's (its lead's ref).
  const [chat, setChat] = useState<string | null>(null);
  // The controls and the legend, asked for.
  const [help, setHelp] = useState(false);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  // Starting or waking an agent from its chat's composer, as its page does.
  const agentAction = useMutation({
    mutationFn: ({ ref, name }: { ref: string; name: 'start' | 'resume' }) => api.agentAction(ref, name),
    onSuccess: (updated) => qc.setQueryData<T.Agent[]>(['agents'], (list) => list?.map((a) => (a.ref === updated.ref ? updated : a))),
  });
  const leave = useRef(onBack);
  leave.current = onBack;
  const reduced = useReducedMotion();
  // The needs list: the user's choice, or else open while the window is wide
  // and you're not at a desk. Arriving somewhere new forgets the choice.
  const [listChoice, setListChoice] = useState<boolean | null>(null);
  // A chip's "+n" opens the list on just its group; null is everyone.
  const [listOnly, setListOnly] = useState<string[] | null>(null);
  // Which list the summary's counts opened: who needs you, who's working,
  // what waits for review.
  const [listKind, setListKind] = useState<'needs' | 'working' | 'review'>('needs');
  const [wide, setWide] = useState(true);
  useEffect(() => {
    const el = host.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setWide(el.clientWidth >= 1200));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const office = useOffice();
  const openReview = useOpenReview();

  const nearAgent = near && !near.endsWith('/lead') && near !== 'board' ? office.agents.get(near) : undefined;
  // What the desk you're at is doing, live: its chat's latest turn, kept
  // current by the chat's events like any open chat.
  const thread = useQuery({ queryKey: nearAgent ? chatKey(nearAgent.ref) : ['chat', '-'], queryFn: () => peekThread(qc, nearAgent!.ref), enabled: !!nearAgent, staleTime: 30_000 });
  const nearThread = nearAgent ? thread.data : undefined;

  // Each desk's monitor in words.
  const toneLabel = (tone: DeskTone) => t(`office.tone.${tone}`);
  const prLine = (pr: PrLook): Screen['lines'][number] => ({
    text: t('office.pr.line', { number: pr.number, state: t(`office.pr.${pr.state}`), checks: pr.checks === 'none' ? '' : ` · ${t(`office.checks.${pr.checks}`)}` }),
    tone: pr.checks === 'failing' ? 'bad' : pr.state === 'merged' ? 'code' : pr.checks === 'passing' ? 'good' : 'muted',
  });
  const screens = useMemo(() => {
    const out = new Map<string, Screen>();
    for (const area of office.model.areas) {
      out.set(leadRef(area.project), {
        tone: area.lead.tone === 'needs' ? 'needs' : area.lead.tone === 'working' ? 'working' : 'idle',
        head: t(`office.lead.${area.lead.tone}`),
        title: t('office.lead.title', { project: area.label }),
        lines: area.lead.line ? [{ text: area.lead.line, tone: 'plain' }] : [{ text: t('office.lead.quiet'), tone: 'muted' }],
      });
      for (const d of area.desks) {
        const lines: Screen['lines'] = [];
        if (d.question) lines.push({ text: d.question, tone: d.tone === 'error' ? 'bad' : 'warn' });
        if (d.id === near && nearThread) {
          const agent = office.agents.get(d.id);
          if (agent) lines.push({ text: `▸ ${agentChatStatus(agent, nearThread).text}`, tone: 'good' });
          const last = lastMessage(nearThread);
          if (last && !d.question) lines.push({ text: last, tone: 'plain' });
        }
        if (d.pr) lines.push(prLine(d.pr));
        if (d.changes) lines.push({ text: t('office.changes', { files: d.changes.files, insertions: d.changes.insertions, deletions: d.changes.deletions }), tone: 'muted' });
        lines.push({ text: d.branch, tone: 'code' });
        out.set(d.id, { tone: d.tone, head: toneLabel(d.tone), title: d.title, lines });
      }
    }
    return out;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [office.model, near, nearThread, t]);

  const boardScreen = useMemo((): BoardScreen => {
    const col = (items: BoardItem[], head: string, empty: string) => ({
      // Its count is added where it's drawn (textures.ts): each room's board
      // shows its own.
      head,
      empty,
      items: items.map((i) => ({ ref: i.ref, title: i.title, sub: subOf(i), tone: i.tone, failing: i.pr?.checks === 'failing' })),
    });
    const label = (project: string) => office.model.areas.find((a) => a.project === project)?.label ?? project;
    const subOf = (i: BoardItem) =>
      i.ref.endsWith('/lead') ? t('office.board.leadWaiting') : i.text ? `${label(i.project)} · ${i.text}` : i.pr ? `${label(i.project)} · #${i.pr.number} ${t(`office.pr.${i.pr.state}`)}${i.pr.checks === 'failing' ? ` · ${t('office.checks.failing')}` : ''}` : label(i.project);
    return {
      title: t('office.board.title'),
      needs: col(office.model.board.needs, t('office.board.needs'), t('office.board.needsEmpty')),
      review: col(office.model.board.review, t('office.board.review'), t('office.board.reviewEmpty')),
    };
  }, [office.model, t]);

  // Each area's label from far off: its name and what's going on in it.
  const areaLabels = useMemo(() => {
    const out = new Map<string, { name: string; sub: string; count: number }>();
    for (const a of office.model.areas) {
      const n = (tone: DeskTone) => a.desks.filter((d) => d.tone === tone).length;
      const needs = n('needs') + n('error') + (a.lead.tone === 'needs' ? 1 : 0);
      const parts = [needs ? t('office.count.needs', { count: needs }) : '', n('working') ? t('office.count.working', { count: n('working') }) : '', n('finished') ? t('office.count.review', { count: n('finished') }) : ''].filter(Boolean);
      out.set(a.project, { name: a.label, sub: parts.length ? parts.join(' · ') : t('office.lead.agents', { count: a.desks.length }), count: needs });
    }
    return out;
  }, [office.model, t]);

  // Opening what you're at: an agent's chat or a project's, in place over
  // the room.
  const activate = useRef<(ref: string) => void>(() => {});
  activate.current = (ref: string) => {
    if (ref === 'board') return;
    setChat(ref);
  };

  // A row on the board: who needs you opens their chat, what waits for
  // review opens its review or pull request.
  const boardRow = useRef<(ref: string) => void>(() => {});
  boardRow.current = (ref: string) => {
    // The board's "+n": the full list, in the panel beside it.
    // It goes to the first row the board had no room for, lit up a moment.
    if (ref.startsWith('more:')) {
      const [, list, from] = ref.split(':');
      const card = document.querySelector<HTMLElement>(`[data-office-board-list="${list}"]`);
      const rows = card ? [...card.querySelectorAll<HTMLElement>('button')] : [];
      const first = rows[Number(from) || 0] ?? rows[0];
      if (!first) return;
      first.scrollIntoView({ block: 'center', behavior: reduced ? 'auto' : 'smooth' });
      first.focus({ preventScroll: true });
      if (!reduced) for (const r of rows.slice(Number(from) || 0)) r.animate([{ backgroundColor: 'rgba(251,191,36,.4)', boxShadow: 'inset 0 0 0 2px rgba(245,158,11,.9)' }, { backgroundColor: 'rgba(251,191,36,.25)', boxShadow: 'inset 0 0 0 2px rgba(245,158,11,.7)', offset: 0.7 }, { backgroundColor: 'rgba(251,191,36,0)', boxShadow: 'inset 0 0 0 2px rgba(245,158,11,0)' }], { duration: 2600, easing: 'ease-out' });
      return;
    }
    // Walk there, as everywhere else: its card up, with the question, E to
    // open its chat and its pull request a button away.
    walkTo(ref);
  };

  // Next walks to the next one that needs you, after the one you're at: a
  // round of everything waiting, one key each.
  // Off the round (at another desk), it carries on from the last one on it.
  const lastNeed = useRef<string | undefined>(undefined);
  if (near && office.model.board.needs.some((i) => i.ref === near)) lastNeed.current = near;
  // N's round goes room by room: each project's floors in turn, in each who
  // needs you before what failed, so it finishes a room before moving on.
  const round = useMemo(() => {
    const placeOf = (i: BoardItem) => {
      const ai = office.model.areas.findIndex((a) => a.project === i.project);
      const a = office.model.areas[ai];
      let floor = 0;
      if (a && !i.ref.endsWith('/lead')) {
        const sizes = floorsOf(a.desks.length);
        let at = a.desks.findIndex((d) => d.id === i.ref);
        while (floor < sizes.length - 1 && at >= sizes[floor]) at -= sizes[floor++];
      }
      return [ai, floor, i.tone === 'needs' ? 0 : 1];
    };
    const keyed = office.model.board.needs.map((i, k) => ({ i, k, p: placeOf(i) }));
    keyed.sort((a, b) => a.p[0] - b.p[0] || a.p[1] - b.p[1] || a.p[2] - b.p[2] || a.k - b.k);
    return keyed.map((x) => x.i);
  }, [office.model]);
  const roundAt = () => {
    const list = round;
    const here = list.findIndex((i) => i.ref === near);
    return here >= 0 ? here : list.findIndex((i) => i.ref === lastNeed.current);
  };
  const next = useRef<() => void>(() => {});
  next.current = () => {
    const list = round;
    if (list.length === 0) return;
    // From a chat, the next one's chat opens as you get there.
    const go = (ref: string) => {
      if (chat) chatNext.current = ref;
      walkTo(ref);
    };
    // Off the round, in a room where someone needs you: them first.
    if (!list.some((i) => i.ref === near) && view.project) {
      const here = list.find((i) => i.project === view.project && floorOfRef(i.project, i.ref) === view.floor);
      if (here) return go(here.ref);
    }
    const to = list[(roundAt() + 1) % list.length];
    if (to.ref !== near) go(to.ref);
  };
  const chatNext = useRef<string | null>(null);
  // Which of its project's floors a desk is on (layout.ts splits them so, in order).
  const floorOfRef = (project: string, ref: string) => {
    const a = office.model.areas.find((x) => x.project === project);
    if (!a || ref.endsWith('/lead')) return 1;
    const sizes = floorsOf(a.desks.length);
    let at = a.desks.findIndex((d) => d.id === ref);
    let floor = 0;
    while (floor < sizes.length - 1 && at >= sizes[floor]) at -= sizes[floor++];
    return floor + 1;
  };

  // The scene, loaded with three.js only when the Office opens.
  useEffect(() => {
    let gone = false;
    let made: OfficeScene | null = null;
    void import('../../office/scene')
      .then((mod) => {
        if (gone || !host.current) return;
        try {
          const handlers: Parameters<typeof mod.createOfficeScene>[1] = {
            reducedMotion: reduced,
            markerLabel: (title) => t('office.walkTo', { title }),
            moreLabel: t('office.showAll'),
            moreLabelShort: (n) => t('office.moreShort', { count: n }),
            leadWord: t('office.lead.word'),
            onNear: setNear,
            onView: setView,
            floorLabel: (name, floor) => t('office.floor', { name, floor }),
            onHover: setHover,
            onActivate: (ref) => activate.current(ref),
            onNext: () => next.current(),
            onBoardRow: (ref) => boardRow.current(ref),
            onMore: (refs) => {
              setListOnly(refs);
              setListKind('needs');
              setListChoice(true);
            },
            onLeave: () => leave.current(),
            onMoved: () => {
              setHint(false);
              try {
                localStorage.setItem(hintKey, '1');
              } catch {
                // Shown again next time, then.
              }
            },
          };
          // The room is built once and kept while the app runs: coming back
          // to the Office is instant, where you left it.
          if (kept) kept.attach(host.current, handlers);
          else kept = mod.createOfficeScene(host.current, handlers);
          made = kept;
          scene.current = made;
          // The preview drives it from its URL (dev/preview.tsx, ?walk=).
          (window as unknown as { officeHook?: (s: OfficeScene) => void }).officeHook?.(made);
          setStatus('ready');
        } catch {
          setStatus('unsupported');
        }
      })
      .catch(() => !gone && setStatus('unsupported'));
    return () => {
      gone = true;
      made?.detach();
      scene.current = null;
    };
    // The scene is made once; reduced motion is passed on below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => scene.current?.setReducedMotion(reduced), [reduced, status]);
  useEffect(() => {
    if (status === 'ready') scene.current?.apply({ model: office.model, screens, board: boardScreen, you: t('office.you'), areaLabels });
  }, [status, office.model, screens, boardScreen, areaLabels, t]);

  const walkTo = (ref: string) => {
    scene.current?.walkTo(ref);
    host.current?.querySelector<HTMLCanvasElement>('canvas')?.focus({ preventScroll: true });
  };
  const openPr = (desk: DeskModel) => {
    const item = office.review.find((r) => r.ref === desk.id);
    if (item && openReview) return openReview({ item });
    const pr = office.fleets.get(desk.project)?.agents.find((a) => a.ref === desk.id)?.pr ?? item?.pr;
    if (pr) setViewingPr({ project: desk.project, pr });
  };

  const { needs, review } = office.model.board;
  const working = office.model.areas.reduce((n, a) => n + a.desks.filter((d) => d.tone === 'working').length, 0);
  const empty = office.loaded && office.model.areas.length === 0;
  const nearDesk = nearAgent ? deskOf(office.model, nearAgent.ref) : undefined;
  const nearArea = near?.endsWith('/lead') ? office.model.areas.find((a) => leadRef(a.project) === near) : undefined;
  const chatAgent = chat && !chat.endsWith('/lead') ? office.agents.get(chat) : undefined;
  const chatProject = chat?.endsWith('/lead') ? projects.data?.find((p) => p.name === chat.split('/')[0]) : undefined;
  const chatOpen = !!(chatAgent || chatProject);
  // The tray of who needs you: folded to its counts until you open it;
  // going to another room folds it again.
  const trayOpen = listChoice ?? false;
  const hasNext = needs.some((i) => i.ref !== near);
  const nextButton = hasNext && (
    <Button size="sm" variant="secondary" onClick={() => next.current()} data-office-next>
      <ArrowRight />
      {t('office.next')}
      {/* Where you are in the round, or at a desk not in it how many wait. */}
      {needs.length > 1 && (
        <span className="tabular-nums text-caption text-muted">
          {round.some((i) => i.ref === near) ? t('office.nextOf', { at: roundAt() + 1, count: needs.length }) : t('office.nextCount', { count: needs.length })}
        </span>
      )}
      <Kbd>N</Kbd>
    </Button>
  );
  useEffect(() => {
    if (lastViewKey.current === viewKey) return;
    lastViewKey.current = viewKey;
    setListChoice(false);
  }, [viewKey]);
  // Escape closes what's open over the room first: the chat, then the tray.
  // Taken, so it doesn't step back in the room too.
  useEffect(() => {
    if (!chatOpen && !(trayOpen && listChoice)) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return;
      if (document.querySelector('[role="dialog"]:not([data-office-chat])')) return;
      e.preventDefault();
      if (chatOpen) setChat(null);
      else setListChoice(false);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [chatOpen, trayOpen, listChoice]);
  useEffect(() => {
    if (!trayOpen) setListOnly(null);
  }, [trayOpen]);
  // The chat closes as you walk away from it, and one asked for by Next
  // opens as you arrive.
  useEffect(() => {
    if (near && chatNext.current === near) {
      chatNext.current = null;
      setChat(near);
    } else if (chat && near !== chat) setChat(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [near]);
  // A close-up keeps what it looks at clear of what's up on the right.
  useEffect(() => {
    if (status !== 'ready') return;
    const w = host.current?.clientWidth ?? 0;
    scene.current?.setCover(chatOpen ? Math.min(w - 24, 480) + 24 : 0);
  }, [chatOpen, status]);
  // The controls, shown a while and then folded to a button.
  useEffect(() => {
    if (!hint) return;
    const id = window.setTimeout(() => setHint(false), 9000);
    return () => window.clearTimeout(id);
  }, [hint]);
  const workingItems: BoardItem[] = office.model.areas.flatMap((a) => a.desks.filter((d) => d.tone === 'working').map((d) => ({ ref: d.id, project: a.project, title: d.title, tone: d.tone, pr: d.pr })));
  const listed = listOnly ? needs.filter((i) => listOnly.includes(i.ref)) : listKind === 'working' ? workingItems : listKind === 'review' ? review : round;
  const areaLabel = (project: string, ref?: string) => {
    const a = office.model.areas.find((x) => x.project === project);
    const name = a?.label ?? project;
    // With floors, which one it's on.
    if (!a || !ref || ref.endsWith('/lead') || floorsOf(a.desks.length).length < 2) return name;
    return t('office.floor', { name, floor: floorOfRef(project, ref) });
  };
  const openList = (kind: 'needs' | 'working' | 'review') => {
    setListOnly(null);
    setListChoice(kind === listKind ? !trayOpen : true);
    setListKind(kind);
  };
  const hoverTitle = hover ? titleOf(office.model, hover, t('office.board.title')) : undefined;
  const hoverText = hoverTitle ? (hover === near && hover !== 'board' ? t('office.clickToOpen', { title: hoverTitle }) : hover === near ? undefined : t('office.clickToWalk', { title: hoverTitle })) : undefined;
  const roomName = view.project ? (office.model.areas.find((a) => a.project === view.project)?.label ?? view.project) : undefined;
  const chatTitle = chatAgent ? (deskOf(office.model, chatAgent.ref)?.title ?? chatAgent.ref) : chatProject ? t('office.lead.title', { project: office.model.areas.find((a) => a.project === chatProject.name)?.label ?? chatProject.name }) : '';
  const chatDesk = chatAgent ? deskOf(office.model, chatAgent.ref) : undefined;
  const chatQuestion = chatDesk?.question && (chatDesk.tone === 'needs' || chatDesk.tone === 'error') ? plain(readable(chatDesk.question)) : undefined;
  const chatTone: DeskTone = chatAgent ? (deskOf(office.model, chatAgent.ref)?.tone ?? 'idle') : nearArea?.lead.tone === 'needs' ? 'needs' : nearArea?.lead.tone === 'working' ? 'working' : 'idle';

  return createPortal(
    <div
      // The whole window, the app's chrome under it: a pale sky for the
      // diorama to float on.
      className="fixed inset-0 z-40 overflow-hidden bg-[linear-gradient(180deg,#e9f2f7_0%,#d5e5ee_100%)] text-primary"
      // The app's light tokens, whatever its theme: the room is a bright
      // diorama, and what's over it white, as the target's pills are.
      data-appearance-scope="light"
      data-office
      // After a click on a button over the room, the keys walk again.
      onClickCapture={(e) => {
        const el = e.target as HTMLElement;
        if (el.closest('button') && !el.closest('[data-office-chat]')) requestAnimationFrame(() => host.current?.querySelector<HTMLCanvasElement>('canvas')?.focus({ preventScroll: true }));
      }}
    >
      <div ref={host} className="absolute inset-0" aria-label={t('office.canvas')} role="application" onPointerDownCapture={() => listChoice && !wide && setListChoice(false)} />

      {(status === 'loading' || (status === 'ready' && !office.loaded)) && (
        <div className="absolute inset-0 flex items-center justify-center">
          <div className={cn(PANEL, 'flex items-center gap-2 px-4 py-2.5 text-ui text-secondary')}>
            <LoaderCircle className="size-4 animate-spin" />
            {t('office.loading')}
          </div>
        </div>
      )}
      {status === 'unsupported' && (
        <div className="absolute inset-0 flex items-center justify-center p-6">
          <div className={cn(PANEL, 'max-w-sm p-5 text-center text-ui text-secondary')}>{t('office.unsupported')}</div>
        </div>
      )}

      {/* The top bar: out of the Office, where you are, and the way back to all the rooms. */}
      <div className="pointer-events-none absolute inset-x-3 top-3 z-20 flex items-center gap-2">
        <nav className={cn(PANEL, 'pointer-events-auto flex h-10 min-w-0 items-center gap-1 px-1.5 text-ui')} aria-label={t('office.title')} data-office-summary data-office-review={review.map((i) => i.ref).join(',')}>
          <BarButton onClick={onBack} title={t('office.leaveHint')} data-office-leave>
            <ArrowLeft />
            {t('office.leave')}
          </BarButton>
          <span className="mx-0.5 h-5 w-px shrink-0 bg-line-strong" />
          {view.project ? (
            <BarButton onClick={() => scene.current?.campus()} title={t('office.campus')} data-office-crumb-campus>
              <span className="font-semibold text-primary">{t('office.title')}</span>
            </BarButton>
          ) : (
            <span className="px-2 font-semibold text-primary">{t('office.title')}</span>
          )}
          {view.project && (
            <>
              <ChevronRight className="size-3.5 shrink-0 text-faint" />
              <span className="min-w-0 truncate px-1 font-medium text-primary" data-office-room={view.project}>
                {view.floors > 1 ? t('office.floor', { name: roomName ?? '', floor: view.floor }) : roomName}
              </span>
            </>
          )}
          {/* A project on floors: straight to another, without the campus. */}
          {view.project && view.floors > 1 && (
            <div className="ml-1 flex items-center gap-0.5 rounded-lg bg-surface p-0.5" role="group" aria-label={t('office.floors')} data-office-floors>
              {Array.from({ length: view.floors }, (_, i) => i + 1).map((f) => (
                <button
                  key={f}
                  type="button"
                  className={cn('h-6 min-w-6 rounded-md px-1.5 text-caption font-semibold tabular-nums transition-colors', f === view.floor ? 'bg-surface-vivid text-primary' : 'text-muted hover:bg-surface-strong hover:text-primary')}
                  aria-current={f === view.floor ? 'true' : undefined}
                  aria-label={t('office.toFloor', { floor: f })}
                  title={t('office.toFloor', { floor: f })}
                  onClick={() => scene.current?.floor(f)}
                >
                  {f}
                </button>
              ))}
            </div>
          )}
        </nav>
        <span className="flex-1" />
        {(view.project || !view.overview) && (
          <div className={cn(PANEL, 'pointer-events-auto flex h-10 items-center gap-1 px-1.5 text-ui')}>
            {near !== 'board' && view.project && (
              <BarButton onClick={() => walkTo('board')} data-office-to-board>
                <ClipboardList />
                {t('office.toBoard')}
              </BarButton>
            )}
            {/* A step back: to the room's overview, from there to all the rooms. */}
            <BarButton onClick={() => (view.project ? scene.current?.campus() : scene.current?.overview())} data-office-overview>
              <Scan />
              {view.project ? t('office.campus') : t('office.overview')}
            </BarButton>
          </div>
        )}
      </div>

      {/* Who needs you, who's working, what waits for review: a tray, each row a walk away. */}
      {status === 'ready' && !empty && !(chatOpen && !wide) && (
        <div className="pointer-events-none absolute bottom-3 left-3 z-10 flex w-[min(22rem,calc(100%-1.5rem))] flex-col items-start" data-office-needs={trayOpen ? 'open' : 'folded'}>
          <div className={cn(PANEL, 'pointer-events-auto flex w-full min-h-0 flex-col')}>
            {/* The list over its tabs: the tabs stay where they are as it opens. */}
            {trayOpen && (
              <div className="max-h-[min(22rem,calc(100vh-12rem))] min-h-0 overflow-y-auto border-b border-line px-1.5 pb-1 pt-1.5" role="tabpanel">
                {listed.length === 0 && <div className="px-2 py-3 text-ui text-muted">{listKind === 'needs' ? t('office.board.needsEmpty') : listKind === 'review' ? t('office.board.reviewEmpty') : t('office.tray.noneWorking')}</div>}
                {listed.map((i) => (
                  <WalkRow
                    key={i.ref}
                    item={i}
                    active={i.ref === near}
                    sub={i.ref.endsWith('/lead') ? t('office.board.leadWaiting') : i.text ? `${areaLabel(i.project, i.ref)} · ${i.text}` : i.pr ? `${areaLabel(i.project, i.ref)} · #${i.pr.number}` : areaLabel(i.project, i.ref)}
                    label={t('office.walkTo', { title: i.title })}
                    onClick={() => {
                      if (!wide) setListChoice(false);
                      walkTo(i.ref);
                    }}
                  />
                ))}
                {listOnly && (
                  <button type="button" className="w-full rounded-lg px-2 py-1.5 text-left text-caption text-amber-300 transition-colors hover:bg-surface-strong" onClick={() => setListOnly(null)} data-office-list-all>
                    {t('office.showAll')} · {needs.length}
                  </button>
                )}
              </div>
            )}
            <div className="flex items-center gap-1 p-1.5" role="tablist" aria-label={t('office.tray')}>
              <TrayTab dot="bg-amber-400" pulse={needs.length > 0} n={needs.length} label={t('office.legend.needs')} on={trayOpen && listKind === 'needs'} onClick={() => openList('needs')} data-office-unfold />
              <TrayTab dot="bg-sky-400" n={working} label={t('office.list.working')} on={trayOpen && listKind === 'working'} onClick={() => openList('working')} />
              <TrayTab dot="bg-emerald-400" n={review.length} label={t('office.tray.review')} on={trayOpen && listKind === 'review'} onClick={() => openList('review')} />
              <span className="flex-1" />
              <button type="button" aria-label={trayOpen ? t('office.fold') : t('office.unfold')} title={trayOpen ? t('office.fold') : t('office.unfold')} onClick={() => setListChoice(!trayOpen)} className="grid size-7 place-items-center rounded-lg text-muted transition-colors hover:bg-surface-strong hover:text-primary" aria-expanded={trayOpen} data-office-fold>
                {trayOpen ? <ChevronDown className="size-4" /> : <ChevronUp className="size-4" />}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* The desk you're at: what it's doing, and its chat a key away. */}
      {!chatOpen && nearDesk && nearAgent && (
        <DeskCard
          tone={nearDesk.tone}
          toneLabel={toneLabel(nearDesk.tone)}
          title={nearDesk.title}
          project={areaLabel(nearDesk.project, nearDesk.id)}
          facts={[nearDesk.branch, [nearAgent.ai, modelOf(nearThread)].filter(Boolean).join(' · ')]}
          activity={nearThread ? agentChatStatus(nearAgent, nearThread).text : undefined}
          question={nearDesk.question ? plain(readable(nearDesk.question)) : nearDesk.tone === 'needs' || nearDesk.tone === 'error' ? agentChatStatus(nearAgent, nearThread).text : undefined}
          last={nearDesk.question ? undefined : lastMessage(nearThread)}
          pr={nearDesk.pr ? prLine(nearDesk.pr).text : undefined}
          prTone={nearDesk.pr?.checks === 'failing' ? 'bad' : 'ok'}
        >
          <Button size="sm" variant="primary" onClick={() => activate.current(nearDesk.id)} data-office-open>
            <MessageSquare />
            {nearDesk.tone === 'needs' ? t('office.answer') : t('office.openChat')}
            <Kbd onBrand>E</Kbd>
          </Button>
          {nextButton}
          {nearDesk.pr && (
            <Button size="sm" variant="secondary" onClick={() => openPr(nearDesk)} data-office-pr>
              <GitPullRequest />
              {t('office.openPr', { number: nearDesk.pr.number })}
            </Button>
          )}
        </DeskCard>
      )}
      {!chatOpen && nearArea && (
        <DeskCard
          tone={nearArea.lead.tone === 'needs' ? 'needs' : nearArea.lead.tone === 'working' ? 'working' : 'idle'}
          toneLabel={t(`office.lead.${nearArea.lead.tone}`)}
          title={t('office.lead.title', { project: nearArea.label })}
          project={t('office.lead.agents', { count: nearArea.desks.length })}
          facts={[]}
          last={nearArea.lead.line ? plain(nearArea.lead.line) : nearArea.lead.line}
        >
          <Button size="sm" variant="primary" onClick={() => activate.current(leadRef(nearArea.project))} data-office-open>
            <MessageSquare />
            {t('office.openProjectChat')}
            <Kbd onBrand>E</Kbd>
          </Button>
          {nextButton}
        </DeskCard>
      )}

      {/* Its chat, in place: the app's own, over the room. */}
      {chatOpen && (
        <section
          className={cn(PANEL, 'pointer-events-auto absolute bottom-3 right-3 top-16 z-20 flex w-[min(30rem,calc(100%-1.5rem))] flex-col overflow-hidden')}
          // Not modal: the room stays in view; but the room's keys rest while you type.
          role="dialog"
          aria-modal="false"
          aria-label={chatTitle}
          data-office-chat={chat ?? ''}
        >
          <header className="flex min-w-0 items-center gap-2 border-b border-line px-3 py-2">
            <span className={cn('size-2 shrink-0 rounded-full', toneDot[chatTone], chatTone === 'needs' && 'animate-pulse')} />
            <div className="min-w-0 flex-1">
              <div className="truncate text-ui font-semibold text-primary">{chatTitle}</div>
              <div className="truncate text-caption text-muted">{chatAgent ? areaLabel(chatAgent.project, chatAgent.ref) : t('office.lead.agents', { count: nearArea?.desks.length ?? 0 })}</div>
            </div>
            {hasNext && (
              <Button size="icon-sm" variant="ghost" title={t('office.next')} aria-label={t('office.next')} onClick={() => next.current()} data-office-next>
                <ArrowRight />
              </Button>
            )}
            <Button
              size="icon-sm"
              variant="ghost"
              title={t('office.chat.openInApp')}
              aria-label={t('office.chat.openInApp')}
              onClick={() => {
                const ref = chat!;
                setChat(null);
                if (ref.endsWith('/lead')) onSelect({ kind: 'project', project: ref.split('/')[0], tab: 'chat', from: 'office' });
                else onSelect({ kind: 'agent', ref, tab: 'chat', from: 'office' });
              }}
              data-office-chat-full
            >
              <Maximize2 />
            </Button>
            <Button size="icon-sm" variant="ghost" title={t('office.chat.close')} aria-label={t('office.chat.close')} onClick={() => setChat(null)} data-office-chat-close>
              <X />
            </Button>
          </header>
          <div className="flex min-h-0 flex-1 flex-col bg-chat">
            {chatAgent && (
              <ChatTab
                key={chatAgent.ref}
                agent={chatAgent}
                starting={agentAction.isPending}
                onStart={() => agentAction.mutate({ ref: chatAgent.ref, name: chatAgent.state === 'paused' ? 'resume' : 'start' })}
                // What it asks, over the composer you answer it in.
                aboveComposer={chatQuestion && (
                  <div className="mb-2 flex max-h-32 min-w-0 gap-2 overflow-y-auto rounded-xl bg-amber-400/10 px-3 py-2 text-ui text-amber-200 ring-1 ring-amber-400/40 [overflow-wrap:anywhere]" data-office-chat-question>
                    <span className="mt-1.5 size-2 shrink-0 animate-pulse rounded-full bg-amber-400" />
                    <span className="min-w-0">{chatQuestion}</span>
                  </div>
                )}
              />
            )}
            {chatProject && <ProjectChatPanel key={chatProject.name} project={chatProject} />}
          </div>
        </section>
      )}

      {/* What a click on what's under the pointer would do. */}
      {hoverText && (
        <div className="pointer-events-none absolute inset-x-0 bottom-3 z-10 flex justify-center px-3">
          <div className={cn(PANEL, 'px-3 py-1.5 text-ui font-medium text-primary')} data-office-hover>
            {hoverText}
          </div>
        </div>
      )}

      {/* The controls, a while, then a button that shows them and the legend. */}
      {status === 'ready' && !empty && !chatOpen && !nearDesk && !nearArea && (
        <div className="pointer-events-none absolute bottom-3 right-3 z-10 flex flex-col items-end gap-2">
          {(hint || help) && (
            <div className={cn(PANEL, 'pointer-events-auto w-[min(19rem,calc(100vw-1.5rem))] p-3 text-caption text-secondary transition-opacity duration-500')} data-office-hint>
              <div className="grid grid-cols-[auto_1fr] items-center gap-x-3 gap-y-1.5">
                {(view.project ? KEYS_ROOM : KEYS_CAMPUS).map(([keys, what]) => (
                  <Fragment key={what}>
                    <span className="flex justify-end gap-1">
                      {keys.map((k) => (
                        <Kbd key={k}>{k.startsWith('=') ? k.slice(1) : t(k as Message)}</Kbd>
                      ))}
                    </span>
                    <span>{t(what)}</span>
                  </Fragment>
                ))}
              </div>
              {help && (
                <div className="mt-3 grid grid-cols-2 gap-x-3 gap-y-1.5 border-t border-line pt-3" data-office-legend>
                  {LEGEND.map(([glyph, color, key]) => (
                    <span key={key} className="flex items-center gap-1.5">
                      <span className="grid size-4 shrink-0 place-items-center rounded-full text-[10px] font-bold leading-none text-white" style={{ background: color }}>
                        {glyph}
                      </span>
                      {t(key)}
                    </span>
                  ))}
                  <span className="col-span-2 flex items-center gap-1.5">
                    <Flag className="size-3.5 shrink-0 text-emerald-400" />
                    {t('office.legend.pr')}
                  </span>
                </div>
              )}
            </div>
          )}
          <button
            type="button"
            className={cn(PANEL, 'pointer-events-auto grid size-9 place-items-center text-muted transition-colors hover:text-primary', help && 'text-primary')}
            aria-label={t('office.help')}
            title={t('office.help')}
            aria-expanded={help}
            onClick={() => {
              setHint(false);
              setHelp(!help);
            }}
            data-office-help
          >
            <Keyboard className="size-4" />
          </button>
        </div>
      )}

      {empty && (
        <div className="absolute inset-0 flex items-center justify-center p-6">
          <div className={cn(PANEL, 'max-w-sm p-5 text-center')} data-office-empty>
            <div className="text-body font-semibold text-primary">{t('office.empty.title')}</div>
            <div className="mt-1 text-ui text-muted">{t('office.empty.body')}</div>
          </div>
        </div>
      )}

      {viewingPr && (
        <PullRequestModal
          project={viewingPr.project}
          pr={viewingPr.pr}
          onClose={() => setViewingPr(null)}
          canMerge={false}
          mergeRunning={false}
          onMerge={() => {}}
          edits={[]}
          onToggleLabel={() => {}}
          onOpenAgent={viewingPr.pr.agent ? () => onSelect({ kind: 'agent', ref: `${viewingPr.project}/${viewingPr.pr.agent}` }) : undefined}
        />
      )}
    </div>,
    document.body,
  );
}

// One look for everything over the room, the app's own floating panel.
const PANEL = 'rounded-xl border border-line-strong bg-overlay shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)] backdrop-blur';

// The controls, as keys and what they do: in a room, and over the campus. A
// key is a message's (a word, translated) or the key's own name.
type Message = Parameters<ReturnType<typeof useT>>[0];
const KEYS_ROOM: [(Message | `=${string}`)[], Message][] = [
  [['office.keys.click'], 'office.keys.walk'],
  [['=W', '=A', '=S', '=D'], 'office.keys.walkKeys'],
  [['office.keys.drag'], 'office.keys.turn'],
  [['office.keys.scroll'], 'office.keys.zoom'],
  [['=E'], 'office.keys.open'],
  [['=N'], 'office.keys.next'],
  [['=Esc'], 'office.keys.back'],
];
const KEYS_CAMPUS: [(Message | `=${string}`)[], Message][] = [
  [['office.keys.click'], 'office.keys.enter'],
  [['office.keys.scroll'], 'office.keys.zoomIn'],
  [['=N'], 'office.keys.next'],
  [['=Esc'], 'office.keys.leave'],
];

function Kbd({ children, onBrand }: { children: ReactNode; onBrand?: boolean }) {
  return <kbd className={cn('rounded-md border px-1.5 py-px font-mono text-[10.5px] leading-4', onBrand ? 'border-white/25 bg-white/15 text-on-brand' : 'border-line-strong bg-surface-strong text-secondary')}>{children}</kbd>;
}

function BarButton({ children, className, ...props }: React.ComponentProps<'button'>) {
  return (
    <button type="button" className={cn('flex h-8 items-center gap-1.5 whitespace-nowrap rounded-lg px-2 text-secondary transition-colors hover:bg-surface-strong hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400 [&_svg]:size-4 [&_svg]:shrink-0', className)} {...props}>
      {children}
    </button>
  );
}

function TrayTab({ dot, n, label, on, onClick, pulse, ...props }: { dot: string; n: number; label: string; on: boolean; onClick: () => void; pulse?: boolean } & Record<`data-${string}`, unknown>) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={on}
      onClick={onClick}
      className={cn('flex h-7 items-center gap-1.5 whitespace-nowrap rounded-lg px-2 text-caption transition-colors', on ? 'bg-surface-vivid text-primary' : 'text-secondary hover:bg-surface-strong hover:text-primary', n === 0 && !on && 'text-muted')}
      {...props}
    >
      <span className={cn('size-2 rounded-full', dot, pulse && 'animate-pulse')} />
      <span className="font-semibold tabular-nums">{n}</span>
      {label}
    </button>
  );
}

function titleOf(model: OfficeModel, ref: string, board: string): string | undefined {
  if (ref === 'board') return board;
  if (ref.endsWith('/lead')) return model.areas.find((a) => leadRef(a.project) === ref)?.label;
  return deskOf(model, ref)?.title;
}

// RowTitle is a row's title; an agent told apart from another of the same
// title by its name (model.ts) keeps the name in view, set apart.
function RowTitle({ item }: { item: BoardItem }) {
  const name = item.ref.split('/')[1];
  const suffix = ` (${name})`;
  const named = item.title.endsWith(suffix);
  return (
    <span className="flex min-w-0 items-baseline gap-1.5">
      <span className="truncate text-ui font-medium text-primary">{named ? item.title.slice(0, -suffix.length) : item.title}</span>
      {named && <span className="shrink-0 font-mono text-[10.5px] text-muted">{name}</span>}
    </span>
  );
}

function WalkRow({ item, sub, label, onClick, active }: { item: BoardItem; sub?: string; label: string; onClick: () => void; active?: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      title={label}
      className={cn('group flex w-full min-w-0 items-start gap-2 rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-surface-strong focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400', active && 'bg-surface-strong ring-1 ring-amber-300/40')}
      aria-current={active ? 'true' : undefined}
      data-office-walk={item.ref}
    >
      <span className={cn('mt-1.5 size-2 shrink-0 rounded-full', toneDot[item.tone])} />
      <span className="min-w-0 flex-1">
        <RowTitle item={item} />
        {sub && <span className="line-clamp-2 text-caption text-muted">{sub}</span>}
      </span>
      <ArrowRight className="mt-1 size-3.5 shrink-0 text-faint transition group-hover:translate-x-0.5 group-hover:text-primary" />
    </button>
  );
}

function DeskCard({
  tone,
  toneLabel,
  title,
  project,
  facts,
  activity,
  question,
  last,
  pr,
  prTone,
  children,
}: {
  tone: DeskTone;
  toneLabel: string;
  title: string;
  project: string;
  facts: string[];
  activity?: string;
  question?: string;
  last?: string;
  pr?: string;
  prTone?: 'ok' | 'bad';
  children: ReactNode;
}) {
  return (
    <div className={cn(PANEL, 'pointer-events-auto absolute right-3 top-16 z-20 w-[min(21rem,calc(100%-1.5rem))] p-4')} data-office-desk={tone}>
      <div className="flex items-center gap-2 text-caption font-semibold uppercase tracking-[0.06em]">
        <span className={cn('size-2 rounded-full', toneDot[tone], tone === 'needs' && 'animate-pulse')} />
        <span className="text-secondary">{toneLabel}</span>
        <span className="ml-auto truncate font-normal normal-case tracking-normal text-muted">{project}</span>
      </div>
      <div className="mt-1.5 line-clamp-2 text-body font-semibold leading-snug text-primary">{title}</div>
      {facts.filter(Boolean).length > 0 && (
        <div className="mt-1 flex min-w-0 flex-wrap gap-x-2 text-caption text-muted">
          {facts.filter(Boolean).map((f) => (
            <span key={f} className="max-w-full truncate font-mono">
              {f}
            </span>
          ))}
        </div>
      )}
      {question && <div className="mt-2.5 max-h-28 overflow-y-auto rounded-lg bg-amber-400/10 px-2.5 py-2 text-ui text-amber-200 ring-1 ring-amber-400/40 [overflow-wrap:anywhere]">{question}</div>}
      {activity && (
        <div className={cn('mt-2.5 flex min-w-0 items-center gap-1.5 text-caption', tone === 'working' ? 'text-sky-300' : tone === 'finished' ? 'text-emerald-300' : tone === 'needs' ? 'text-amber-300' : tone === 'error' ? 'text-rose-300' : 'text-muted')}>
          <span className={cn('size-1.5 shrink-0 rounded-full', toneDot[tone])} />
          <span className="min-w-0 truncate">{activity}</span>
        </div>
      )}
      {last && <div className="mt-1.5 line-clamp-3 text-ui text-secondary [overflow-wrap:anywhere]">{last}</div>}
      {pr && (
        <div className={cn('mt-2 flex items-center gap-1.5 text-caption', prTone === 'bad' ? 'text-rose-300' : 'text-secondary')}>
          <GitPullRequest className="size-3.5" />
          {pr}
        </div>
      )}
      <div className="mt-3 flex flex-wrap gap-2">{children}</div>
    </div>
  );
}
