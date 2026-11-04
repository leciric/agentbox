// The Office: a 3D room with a desk for every agent of every project, where
// you can walk about and watch them work (office/scene.ts draws it). This is
// its data, read from the queries the rest of the app keeps live, and the
// overlay around the canvas: who needs you, the desk you're at, the controls.
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowRight, ChevronDown, ClipboardList, ChevronUp, Eye, GitPullRequest, Keyboard, LoaderCircle, MessageSquare, Mouse, Scan, X } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
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
import { deskOf, leadRef, officeModel, type BoardItem, type DeskModel, type DeskTone, type OfficeModel, type PrLook } from '../../office/model';
import type { OfficeScene } from '../../office/scene';
import type { BoardScreen, Screen } from '../../office/textures';
import { PullRequestModal } from '../PullRequestModal';
import { Button } from '../ui/button';
import { useOpenReview } from '../v2/ReviewV2';

const hintKey = 'agentbox.office.hintSeen';

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

export function OfficeView({ onSelect }: { onSelect: (view: View) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const host = useRef<HTMLDivElement>(null);
  const scene = useRef<OfficeScene | null>(null);
  const [status, setStatus] = useState<'loading' | 'ready' | 'unsupported'>('loading');
  const [near, setNear] = useState<string | undefined>();
  const [hover, setHover] = useState<string | undefined>();
  const [hint, setHint] = useState(() => {
    try {
      return localStorage.getItem(hintKey) !== '1';
    } catch {
      return true;
    }
  });
  const [viewingPr, setViewingPr] = useState<{ project: string; pr: T.PullRequest } | null>(null);
  const reduced = useReducedMotion();
  // The needs list: the user's choice, or else open while the window is wide
  // and you're not at a desk. Arriving somewhere new forgets the choice.
  const [listChoice, setListChoice] = useState<boolean | null>(null);
  // A chip's "+n" opens the list on just its group; null is everyone.
  const [listOnly, setListOnly] = useState<string[] | null>(null);
  const [wide, setWide] = useState(true);
  useEffect(() => {
    const el = host.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setWide(el.clientWidth >= 1200));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  useEffect(() => {
    if (near) setListChoice(null);
  }, [near]);
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
      head: `${head} · ${items.length}`,
      empty,
      items: items.slice(0, 12).map((i) => ({ ref: i.ref, title: i.title, sub: subOf(i), tone: i.tone })),
    });
    const label = (project: string) => office.model.areas.find((a) => a.project === project)?.label ?? project;
    const subOf = (i: BoardItem) =>
      i.ref.endsWith('/lead') ? t('office.board.leadWaiting') : i.text ? `${label(i.project)} · ${i.text}` : i.pr ? `${label(i.project)} · #${i.pr.number} ${t(`office.pr.${i.pr.state}`)}` : label(i.project);
    return {
      title: t('office.board.title'),
      needs: col(office.model.board.needs, t('office.board.needs'), t('office.board.needsEmpty')),
      review: col(office.model.board.review, t('office.board.review'), t('office.board.reviewEmpty')),
    };
  }, [office.model, t]);

  // Each area's label from far off: its name and what's going on in it.
  const areaLabels = useMemo(() => {
    const out = new Map<string, { name: string; sub: string }>();
    for (const a of office.model.areas) {
      const n = (tone: DeskTone) => a.desks.filter((d) => d.tone === tone).length;
      const needs = n('needs') + n('error') + (a.lead.tone === 'needs' ? 1 : 0);
      const parts = [needs ? t('office.count.needs', { count: needs }) : '', n('working') ? t('office.count.working', { count: n('working') }) : '', n('finished') ? t('office.count.review', { count: n('finished') }) : ''].filter(Boolean);
      out.set(a.project, { name: a.label, sub: parts.length ? parts.join(' · ') : t('office.lead.agents', { count: a.desks.length }) });
    }
    return out;
  }, [office.model, t]);

  // Opening what you're at: an agent's chat, a project's chat, or the board's list.
  const activate = useRef<(ref: string) => void>(() => {});
  activate.current = (ref: string) => {
    if (ref === 'board') return;
    if (ref.endsWith('/lead')) onSelect({ kind: 'project', project: ref.split('/')[0], tab: 'chat' });
    else onSelect({ kind: 'agent', ref, tab: 'chat', from: 'office' });
  };

  // A row on the board: who needs you opens their chat, what waits for
  // review opens its review or pull request.
  const boardRow = useRef<(ref: string) => void>(() => {});
  boardRow.current = (ref: string) => {
    // The board's "+n": the full list, in the panel beside it.
    if (ref.startsWith('more:')) {
      const card = document.querySelector<HTMLElement>(`[data-office-board-list="${ref.slice(5)}"]`);
      card?.scrollIntoView({ block: 'start', behavior: reduced ? 'auto' : 'smooth' });
      card?.querySelector<HTMLElement>('button')?.focus({ preventScroll: true });
      return;
    }
    const desk = deskOf(office.model, ref);
    if (desk && office.model.board.review.some((i) => i.ref === ref) && desk.pr) openPr(desk);
    else activate.current(ref);
  };

  // Next walks to the next one that needs you, after the one you're at: a
  // round of everything waiting, one key each.
  // Off the round (at another desk), it carries on from the last one on it.
  const lastNeed = useRef<string | undefined>(undefined);
  if (near && office.model.board.needs.some((i) => i.ref === near)) lastNeed.current = near;
  const roundAt = () => {
    const list = office.model.board.needs;
    const here = list.findIndex((i) => i.ref === near);
    return here >= 0 ? here : list.findIndex((i) => i.ref === lastNeed.current);
  };
  const next = useRef<() => void>(() => {});
  next.current = () => {
    const list = office.model.board.needs;
    if (list.length === 0) return;
    const to = list[(roundAt() + 1) % list.length];
    if (to.ref !== near) walkTo(to.ref);
  };

  // The scene, loaded with three.js only when the Office opens.
  useEffect(() => {
    let gone = false;
    let made: OfficeScene | null = null;
    void import('../../office/scene')
      .then((mod) => {
        if (gone || !host.current) return;
        try {
          made = mod.createOfficeScene(host.current, {
            reducedMotion: reduced,
            markerLabel: (title) => t('office.walkTo', { title }),
            moreLabel: t('office.showAll'),
            onNear: setNear,
            onHover: setHover,
            onActivate: (ref) => activate.current(ref),
            onNext: () => next.current(),
            onBoardRow: (ref) => boardRow.current(ref),
            onMore: (refs) => {
              setListOnly(refs);
              setListChoice(true);
            },
            onMoved: () => {
              setHint(false);
              try {
                localStorage.setItem(hintKey, '1');
              } catch {
                // Shown again next time, then.
              }
            },
          });
          scene.current = made;
          setStatus('ready');
        } catch {
          setStatus('unsupported');
        }
      })
      .catch(() => !gone && setStatus('unsupported'));
    return () => {
      gone = true;
      made?.dispose();
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
  const listOpen = listChoice ?? (wide && !nearDesk && !nearArea);
  const hasNext = needs.some((i) => i.ref !== near);
  const nextButton = hasNext && (
    <Button size="sm" variant="secondary" onClick={() => next.current()} data-office-next>
      <ArrowRight className="size-3.5" />
      {t('office.next')}
      {needs.length > 1 && needs.some((i) => i.ref === near) && <span className="tabular-nums text-[11px] opacity-70">{t('office.nextOf', { at: needs.findIndex((i) => i.ref === near) + 1, count: needs.length })}</span>}
      <kbd className="ml-1 rounded bg-black/20 px-1 font-mono text-[10.5px]">N</kbd>
    </Button>
  );
  const listShown = listOpen && needs.length > 0 && near !== 'board';
  // Escape, or a press on the room, folds it away.
  useEffect(() => {
    if (!listShown) return;
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setListChoice(false);
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [listShown]);
  useEffect(() => {
    if (!listOpen) setListOnly(null);
  }, [listOpen]);
  const listed = listOnly ? needs.filter((i) => listOnly.includes(i.ref)) : needs;
  const hoverTitle = hover ? titleOf(office.model, hover, t('office.board.title')) : undefined;
  const hoverText = hoverTitle ? (hover === near && hover !== 'board' ? t('office.clickToOpen', { title: hoverTitle }) : hover === near ? undefined : t('office.clickToWalk', { title: hoverTitle })) : undefined;

  return (
    <div
      className="relative h-full min-h-0 w-full overflow-hidden bg-[#1c2333]"
      data-office
      // After a click on a button over the room, the keys walk again.
      onClickCapture={(e) => {
        if ((e.target as HTMLElement).closest('button')) requestAnimationFrame(() => host.current?.querySelector<HTMLCanvasElement>('canvas')?.focus({ preventScroll: true }));
      }}
    >
      <div ref={host} className="absolute inset-0" aria-label={t('office.canvas')} role="application" onPointerDownCapture={() => listShown && setListChoice(false)} />

      {(status === 'loading' || (status === 'ready' && !office.loaded)) && (
        <div className="absolute inset-0 flex items-center justify-center gap-2 text-[13px] text-slate-300">
          <LoaderCircle className="size-4 animate-spin" />
          {t('office.loading')}
        </div>
      )}
      {status === 'unsupported' && (
        <div className="absolute inset-0 flex items-center justify-center p-6">
          <div className="max-w-sm rounded-xl border border-white/10 bg-slate-900/90 p-5 text-center text-[13px] text-slate-200">{t('office.unsupported')}</div>
        </div>
      )}

      {/* The top: what's going on, and the overview. */}
      <div className="pointer-events-none absolute inset-x-0 top-0 flex flex-wrap items-start gap-2 p-3">
        <div className="pointer-events-auto flex items-center gap-3 rounded-xl border border-white/10 bg-slate-950/70 px-3 py-2 text-[12.5px] text-slate-200 shadow-lg backdrop-blur-md" data-office-summary data-office-review={review.map((i) => i.ref).join(",")}>
          <span className="text-[13px] font-semibold text-white">{t('office.title')}</span>
          {needs.length > 0 && near !== 'board' ? (
            // The count opens and folds the list of who needs you.
            <button
              type="button"
              className="-my-1 flex items-center gap-1.5 whitespace-nowrap rounded-lg px-1.5 py-1 font-medium text-amber-100 ring-1 ring-amber-300/30 transition hover:bg-amber-400/10"
              onClick={() => setListChoice(!listOpen)}
              aria-expanded={listOpen}
              data-office-unfold
            >
              <span className="size-2 animate-pulse rounded-full bg-amber-400" />
              {t('office.count.needs', { count: needs.length })}
              {listOpen ? <ChevronUp className="size-3.5 text-amber-200/70" /> : <ChevronDown className="size-3.5 text-amber-200/70" />}
            </button>
          ) : (
            <Count dot="bg-amber-400" n={needs.length} label={t('office.count.needs', { count: needs.length })} />
          )}
          <Count dot="bg-sky-400" n={working} label={t('office.count.working', { count: working })} />
          <Count dot="bg-emerald-400" n={review.length} label={t('office.count.review', { count: review.length })} />
        </div>
        <span className="flex-1" />
        {near !== 'board' && (
          <button
            type="button"
            className="pointer-events-auto flex items-center gap-1.5 rounded-xl border border-white/10 bg-slate-950/70 px-3 py-2 text-[12.5px] text-slate-200 shadow-lg backdrop-blur-md transition hover:bg-slate-900 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/60"
            onClick={() => walkTo('board')}
            data-office-to-board
          >
            <ClipboardList className="size-3.5" />
            {t('office.toBoard')}
          </button>
        )}
        <button
          type="button"
          className="pointer-events-auto flex items-center gap-1.5 rounded-xl border border-white/10 bg-slate-950/70 px-3 py-2 text-[12.5px] text-slate-200 shadow-lg backdrop-blur-md transition hover:bg-slate-900 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/60"
          onClick={() => scene.current?.overview()}
          data-office-overview
        >
          <Scan className="size-3.5" />
          {t('office.overview')}
        </button>
      </div>

      {/* Who needs you, each a walk away: open while there's room for it,
          folded to a pill at a desk or in a small window. */}
      {listOpen && needs.length > 0 && near !== 'board' && (
        <div className="pointer-events-auto absolute left-3 top-16 flex max-h-[calc(100%-9rem)] w-[min(16rem,calc(100%-1.5rem))] flex-col items-start gap-2 overflow-y-auto" data-office-needs="open">
          <ListCard title={listOnly ? t('office.here', { count: listed.length }) : t('office.board.needs')} accent="text-amber-300" onFold={() => setListChoice(false)} foldLabel={t('office.fold')}>
            {listed.map((i) => (
              <WalkRow key={i.ref} item={i} active={i.ref === near} sub={i.ref.endsWith('/lead') ? t('office.board.leadWaiting') : i.text} label={t('office.walkTo', { title: i.title })} onClick={() => {
                setListChoice(false);
                walkTo(i.ref);
              }} />
            ))}
            {listOnly && (
              <button type="button" className="w-full rounded-lg px-2 py-1.5 text-left text-[12px] text-amber-200 hover:bg-white/10" onClick={() => setListOnly(null)} data-office-list-all>
                {t('office.showAll')} · {needs.length}
              </button>
            )}
          </ListCard>
        </div>
      )}

      {/* The desk you're at. */}
      {nearDesk && nearAgent && (
        <DeskCard
          tone={nearDesk.tone}
          toneLabel={toneLabel(nearDesk.tone)}
          title={nearDesk.title}
          project={office.model.areas.find((a) => a.project === nearDesk.project)?.label ?? nearDesk.project}
          facts={[nearDesk.branch, [nearAgent.ai, modelOf(nearThread)].filter(Boolean).join(' · ')]}
          activity={nearThread ? agentChatStatus(nearAgent, nearThread).text : undefined}
          question={nearDesk.question ?? (nearDesk.tone === 'needs' || nearDesk.tone === 'error' ? agentChatStatus(nearAgent, nearThread).text : undefined)}
          last={nearDesk.question ? undefined : lastMessage(nearThread)}
          pr={nearDesk.pr ? prLine(nearDesk.pr).text : undefined}
          prTone={nearDesk.pr?.checks === 'failing' ? 'bad' : 'ok'}
        >
          <Button size="sm" onClick={() => activate.current(nearDesk.id)} data-office-open>
            <MessageSquare className="size-3.5" />
            {t('office.openChat')}
            <kbd className="ml-1 rounded bg-black/20 px-1 font-mono text-[10.5px]">E</kbd>
          </Button>
          {nextButton}
          {nearDesk.pr && (
            <Button size="sm" variant="secondary" onClick={() => openPr(nearDesk)} data-office-pr>
              <GitPullRequest className="size-3.5" />
              {t('office.openPr', { number: nearDesk.pr.number })}
            </Button>
          )}
        </DeskCard>
      )}
      {nearArea && (
        <DeskCard
          tone={nearArea.lead.tone === 'needs' ? 'needs' : nearArea.lead.tone === 'working' ? 'working' : 'idle'}
          toneLabel={t(`office.lead.${nearArea.lead.tone}`)}
          title={t('office.lead.title', { project: nearArea.label })}
          project={t('office.lead.agents', { count: nearArea.desks.length })}
          facts={[]}
          last={nearArea.lead.line}
        >
          <Button size="sm" onClick={() => activate.current(leadRef(nearArea.project))} data-office-open>
            <MessageSquare className="size-3.5" />
            {t('office.openProjectChat')}
            <kbd className="ml-1 rounded bg-black/20 px-1 font-mono text-[10.5px]">E</kbd>
          </Button>
          {nextButton}
        </DeskCard>
      )}
      {near === 'board' && (
        <div className="pointer-events-auto absolute bottom-16 right-3 top-16 flex w-[min(18rem,calc(100%-1.5rem))] flex-col gap-2 overflow-y-auto" data-office-board>
          <ListCard title={t('office.board.needs')} accent="text-amber-300" list="needs">
            {needs.length === 0 && <Empty>{t('office.board.needsEmpty')}</Empty>}
            {needs.map((i) => (
              <WalkRow key={i.ref} item={i} sub={i.ref.endsWith('/lead') ? t('office.board.leadWaiting') : i.text} label={t('office.open', { title: i.title })} onClick={() => activate.current(i.ref)} />
            ))}
          </ListCard>
          <ListCard title={t('office.board.review')} accent="text-emerald-300" list="review">
            {review.length === 0 && <Empty>{t('office.board.reviewEmpty')}</Empty>}
            {review.map((i) => {
              const desk = deskOf(office.model, i.ref);
              return (
                <WalkRow
                  key={i.ref}
                  item={i}
                  sub={i.pr ? `#${i.pr.number} · ${t(`office.pr.${i.pr.state}`)}${i.pr.checks === 'failing' ? ` · ${t('office.checks.failing')}` : ''}` : toneLabel(i.tone)}
                  label={t('office.review', { title: i.title })}
                  onClick={() => (desk?.pr ? openPr(desk) : activate.current(i.ref))}
                />
              );
            })}
          </ListCard>
        </div>
      )}

      {/* The controls, until you've moved; after that a line. */}
      {status === 'ready' && !empty && (
        <div className="pointer-events-none absolute inset-x-0 bottom-0 flex flex-col items-center gap-2 p-3">
          {/* One bar: what a click on what's under the pointer does, or else the controls. */}
          {hoverText && !hint ? (
            <div className="rounded-lg bg-slate-950/85 px-3 py-1 text-[12.5px] font-medium text-white shadow-lg backdrop-blur-md" data-office-hover>{hoverText}</div>
          ) : hint ? (
            <div className="pointer-events-auto flex max-w-full flex-wrap items-center justify-center gap-x-5 gap-y-1.5 rounded-xl border border-violet-300/30 bg-slate-950/90 px-4 py-2.5 text-[13px] font-medium text-white shadow-xl ring-1 ring-violet-400/20 backdrop-blur-md" data-office-hint>
              <span className="flex items-center gap-1.5"><Mouse className="size-3.5 text-violet-300" />{t('office.hint.click')}</span>
              <span className="flex items-center gap-1.5"><Keyboard className="size-3.5 text-violet-300" />{t('office.hint.keys')}</span>
              <span className="flex items-center gap-1.5"><Eye className="size-3.5 text-violet-300" />{t('office.hint.look')}</span>
              <button type="button" aria-label={t('common.close')} className="rounded p-0.5 text-slate-400 hover:text-white" onClick={() => setHint(false)}>
                <X className="size-3.5" />
              </button>
            </div>
          ) : (
            <div className="rounded-lg bg-slate-950/75 px-3 py-1 text-[12px] text-slate-200 backdrop-blur-md" data-office-hint-short>{t('office.hint.short')}</div>
          )}
        </div>
      )}

      {empty && (
        <div className="absolute inset-0 flex items-center justify-center p-6">
          <div className="max-w-sm rounded-xl border border-white/10 bg-slate-950/80 p-5 text-center shadow-xl backdrop-blur-md" data-office-empty>
            <div className="text-[14px] font-semibold text-white">{t('office.empty.title')}</div>
            <div className="mt-1 text-[12.5px] text-slate-300">{t('office.empty.body')}</div>
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
    </div>
  );
}

function titleOf(model: OfficeModel, ref: string, board: string): string | undefined {
  if (ref === 'board') return board;
  if (ref.endsWith('/lead')) return model.areas.find((a) => leadRef(a.project) === ref)?.label;
  return deskOf(model, ref)?.title;
}

function Count({ dot, n, label }: { dot: string; n: number; label: string }) {
  return (
    <span className={cn('flex items-center gap-1.5 whitespace-nowrap', n === 0 && 'opacity-50')}>
      <span className={cn('size-2 rounded-full', dot, n > 0 && dot === 'bg-amber-400' && 'animate-pulse')} />
      {label}
    </span>
  );
}

function ListCard({ title, accent, children, onFold, foldLabel, list }: { title: string; accent: string; children: ReactNode; onFold?: () => void; foldLabel?: string; list?: string }) {
  return (
    <div className="w-full rounded-xl border border-white/10 bg-slate-950/75 p-1.5 shadow-xl backdrop-blur-md" data-office-board-list={list}>
      <div className="flex items-center px-2 pb-1 pt-1">
        <span className={cn('flex-1 text-[10.5px] font-semibold uppercase tracking-[0.08em]', accent)}>{title}</span>
        {onFold && (
          <button type="button" aria-label={foldLabel} title={foldLabel} onClick={onFold} className="rounded p-0.5 text-slate-400 hover:text-white" aria-expanded data-office-fold>
            <ChevronUp className="size-3.5" />
          </button>
        )}
      </div>
      {children}
    </div>
  );
}

function Empty({ children }: { children: ReactNode }) {
  return <div className="px-2 pb-2 text-[12px] text-slate-400">{children}</div>;
}

// RowTitle is a row's title; an agent told apart from another of the same
// title by its name (model.ts) keeps the name in view, set apart.
function RowTitle({ item }: { item: BoardItem }) {
  const name = item.ref.split('/')[1];
  const suffix = ` (${name})`;
  const named = item.title.endsWith(suffix);
  return (
    <span className="flex min-w-0 items-baseline gap-1.5">
      <span className="truncate text-[12.5px] font-medium text-white">{named ? item.title.slice(0, -suffix.length) : item.title}</span>
      {named && <span className="shrink-0 font-mono text-[10.5px] text-slate-400">{name}</span>}
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
      className={cn('group flex w-full min-w-0 items-start gap-2 rounded-lg px-2 py-1.5 text-left transition hover:bg-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/60', active && 'bg-white/10 ring-1 ring-amber-300/40')}
      aria-current={active ? 'true' : undefined}
      data-office-walk={item.ref}
    >
      <span className={cn('mt-1.5 size-2 shrink-0 rounded-full', toneDot[item.tone])} />
      <span className="min-w-0 flex-1">
        <RowTitle item={item} />
        {sub && <span className="line-clamp-2 text-[11.5px] text-slate-400">{sub}</span>}
      </span>
      <ArrowRight className="mt-1 size-3.5 shrink-0 text-slate-500 transition group-hover:translate-x-0.5 group-hover:text-white" />
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
    <div className="pointer-events-auto absolute bottom-16 right-3 w-[min(21rem,calc(100%-1.5rem))] rounded-2xl border border-white/10 bg-slate-950/85 p-4 text-slate-200 shadow-2xl backdrop-blur-md" data-office-desk={tone}>
      <div className="flex items-center gap-2 text-[11.5px] font-semibold uppercase tracking-[0.06em]">
        <span className={cn('size-2 rounded-full', toneDot[tone], tone === 'needs' && 'animate-pulse')} />
        <span className="text-slate-300">{toneLabel}</span>
        <span className="ml-auto truncate font-normal normal-case tracking-normal text-slate-400">{project}</span>
      </div>
      <div className="mt-1.5 line-clamp-2 text-[14.5px] font-semibold leading-snug text-white">{title}</div>
      {facts.filter(Boolean).length > 0 && (
        <div className="mt-1 flex min-w-0 flex-wrap gap-x-2 text-[11.5px] text-slate-400">
          {facts.filter(Boolean).map((f) => (
            <span key={f} className="max-w-full truncate font-mono">{f}</span>
          ))}
        </div>
      )}
      {question && <div className="mt-2.5 max-h-28 overflow-y-auto rounded-lg bg-amber-400/10 px-2.5 py-2 text-[12.5px] text-amber-100 ring-1 ring-amber-400/30 [overflow-wrap:anywhere]">{question}</div>}
      {activity && <div className="mt-2.5 flex min-w-0 items-center gap-1.5 text-[12px] text-emerald-300"><span className="size-1.5 shrink-0 rounded-full bg-emerald-400" /><span className="min-w-0 truncate">{activity}</span></div>}
      {last && <div className="mt-1.5 line-clamp-3 text-[12.5px] text-slate-300 [overflow-wrap:anywhere]">{last}</div>}
      {pr && <div className={cn('mt-2 flex items-center gap-1.5 text-[12px]', prTone === 'bad' ? 'text-red-300' : 'text-slate-300')}><GitPullRequest className="size-3.5" />{pr}</div>}
      <div className="mt-3 flex flex-wrap gap-2">{children}</div>
    </div>
  );
}
