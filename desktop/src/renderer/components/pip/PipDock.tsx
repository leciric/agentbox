import { useQuery } from '@tanstack/react-query';
import { ArrowUpRight, ChevronLeft, ChevronRight, Layers, Maximize2, Minimize2, PanelRightOpen, Rows3 } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode, type RefObject } from 'react';
import * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { clampSpot, docked, type Spot } from '../../lib/pip';
import { setDock, unfloat, useDock } from '../../lib/pipStore';
import { openPanelOn, useChatPanel } from '../../lib/rightPanel';
import { cn } from '../../lib/utils';
import { Button } from '../ui/button';
import { Tip } from '../ui/tooltip';
import { useOpenReview, useProjectReview } from '../v2/ReviewV2';
import { LiveDesktop } from './LiveDesktop';

// PipDock is the lead's chat's picture-in-picture desktops: a tile for each
// agent at work at the chat's right edge, live. A tile can be dragged out to
// float anywhere over the chat, grown over it, or pinned beside it, which is
// the right-hand panel's Desktop tab; the dock can be stacked into a deck or
// hidden to a tab at the edge. Review stays in the review modal.
export const TILE = 208;
const tileHeight = (TILE * 10) / 16 + 24;
// The chat's text column (ChatTab's max-w-4xl): beside it the dock is out of
// the way, over it the dock stacks.
const textColumn = 896;

export function PipDock({ project, onSelect }: { project: string; onSelect: (view: View) => void }) {
  const t = useT();
  const area = useRef<HTMLDivElement>(null);
  const size = useSize(area);
  const dock = useDock(project);
  const chat = `${project}/${T.LeadName}`;
  const panel = useChatPanel(chat);
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const review = useProjectReview(project);
  const now = useMinute();
  const tiles = docked(project, agents.data ?? [], review.data?.items ?? [], now);
  // The agent whose desktop is open in the panel beside the chat has no tile too.
  const pinned = panel.open ? panel.tabs.find((tab) => tab.id === panel.active && tab.kind === 'desktop')?.target : undefined;
  const shown = tiles.filter((d) => d.agent.ref !== pinned);
  const big = shown.find((d) => d.agent.ref === dock.big);
  const floating = shown.filter((d) => dock.floats[d.agent.ref] && d !== big);
  const inDock = shown.filter((d) => !dock.floats[d.agent.ref] && d !== big);
  const room = (size.width - textColumn) / 2 >= TILE + 24 && size.height >= 40 + inDock.length * (tileHeight + 10);
  const stacked = dock.stacked ?? !room;

  const grow = (ref: string) => setDock(project, (d) => ({ big: ref, floats: unfloat(d, ref) }));
  const pin = (ref: string) => {
    setDock(project, (d) => ({ big: d.big === ref ? null : d.big, floats: unfloat(d, ref) }));
    openPanelOn(chat, 'desktop', ref);
  };
  const actions = { grow, pin, open: (ref: string) => onSelect({ kind: 'agent', ref }) };

  return (
    <div ref={area} className="pointer-events-none absolute inset-0 z-20 overflow-hidden" data-pip>
      {inDock.length > 0 &&
        (dock.hidden ? (
          <button
            type="button"
            onClick={() => setDock(project, () => ({ hidden: false }))}
            className="pointer-events-auto absolute right-0 top-4 flex items-center gap-1.5 rounded-l-xl border border-r-0 border-line-strong bg-overlay py-2 pl-2.5 pr-2 text-[12px] text-secondary shadow-lg backdrop-blur-xl hover:text-title"
            data-pip-dock="hidden"
          >
            <ChevronLeft className="size-3.5" />
            <span className="size-1.5 animate-pulse rounded-full bg-rose-400" />
            {t('pip.live', { count: inDock.length })}
          </button>
        ) : (
          <Dock project={project} tiles={inDock} atWork={tiles.filter((d) => !d.finishedAt).length} stacked={stacked} maxHeight={size.height - 48} area={area} actions={actions} />
        ))}
      {floating.map((d) => (
        <Floating key={d.agent.ref} project={project} agent={d.agent} finishedAt={d.finishedAt} at={clampSpot(dock.floats[d.agent.ref], TILE, tileHeight, size.width, size.height)} area={area} actions={actions} />
      ))}
      {big && <Big project={project} agent={big.agent} finishedAt={big.finishedAt} area={area} actions={actions} />}
    </div>
  );
}

type Actions = { grow: (ref: string) => void; pin: (ref: string) => void; open: (ref: string) => void };

function Dock({ project, tiles, atWork, stacked, maxHeight, area, actions }: { project: string; tiles: { agent: T.Agent; finishedAt?: string }[]; atWork: number; stacked: boolean; maxHeight: number; area: RefObject<HTMLDivElement | null>; actions: Actions }) {
  const t = useT();
  const [fan, setFan] = useState(false);
  const deck = stacked && !fan;
  return (
    <div className="pointer-events-auto absolute right-3 top-3 md:right-4" style={{ width: TILE }} data-pip-dock={stacked ? 'stacked' : 'column'} onMouseLeave={() => setFan(false)}>
      <div className="mb-1.5 flex items-center gap-1 px-0.5 text-[11px] text-subtle">
        <span className="size-1.5 animate-pulse rounded-full bg-rose-400" />
        <span className="flex-1">{t('pip.atWork', { count: atWork })}</span>
        <Tip label={stacked ? t('pip.spread') : t('pip.stack')}>
          <button type="button" onClick={() => setDock(project, () => ({ stacked: !stacked }))} className="rounded p-1 hover:bg-surface-raised hover:text-primary" data-pip-stack>
            {stacked ? <Rows3 className="size-3.5" /> : <Layers className="size-3.5" />}
          </button>
        </Tip>
        <Tip label={t('pip.hide')}>
          <button type="button" onClick={() => setDock(project, () => ({ hidden: true }))} className="rounded p-1 hover:bg-surface-raised hover:text-primary" data-pip-hide>
            <ChevronRight className="size-3.5" />
          </button>
        </Tip>
      </div>
      {/* Fanned out, a stack hangs over the chat rather than squeezing in. */}
      {/* Spread taller than the chat, the column scrolls. */}
      <div className={cn('relative', !deck && 'overflow-y-auto [scrollbar-width:none]')} style={{ height: deck ? tileHeight + Math.min(tiles.length - 1, 2) * 9 : undefined, maxHeight: deck ? undefined : maxHeight }} onMouseEnter={() => stacked && setFan(true)}>
        {tiles.map((d, i) => (
          <DockTile key={d.agent.ref} project={project} agent={d.agent} finishedAt={d.finishedAt} index={i} deck={deck} count={tiles.length} area={area} actions={actions} />
        ))}
      </div>
    </div>
  );
}

function DockTile({ project, agent, finishedAt, index, deck, count, area, actions }: { project: string; agent: T.Agent; finishedAt?: string; index: number; deck: boolean; count: number; area: RefObject<HTMLDivElement | null>; actions: Actions }) {
  const self = useRef<HTMLDivElement>(null);
  const drag = useDrag(area, self, {
    onClick: () => actions.grow(agent.ref),
    // Let go near the dock, it stays docked.
    onDrop: (at) => at.x < (area.current?.clientWidth ?? 0) - TILE * 1.6 && setDock(project, (d) => ({ floats: { ...d.floats, [agent.ref]: at } })),
  });
  const style: React.CSSProperties = deck
    ? { position: 'absolute', top: index * 9, left: 0, right: 0, zIndex: count - index, transform: `scale(${1 - Math.min(index, 3) * 0.05})`, transformOrigin: 'top center', opacity: index > 2 ? 0 : 1 }
    : { position: 'relative', marginBottom: 10 };
  return (
    <>
      <div ref={self} style={{ ...style, visibility: drag.at ? 'hidden' : undefined, transition: 'transform .2s, top .2s' }} {...drag.handlers} data-pip-tile={agent.name}>
        <Tile project={project} agent={agent} finishedAt={finishedAt} paused={deck && index > 0} actions={actions} />
      </div>
      {drag.at && (
        <div className="pointer-events-none fixed z-50 rotate-1 opacity-95" style={{ left: (area.current?.getBoundingClientRect().left ?? 0) + drag.at.x, top: (area.current?.getBoundingClientRect().top ?? 0) + drag.at.y, width: TILE }}>
          <Tile project={project} agent={agent} finishedAt={finishedAt} actions={actions} />
        </div>
      )}
    </>
  );
}

function Floating({ project, agent, finishedAt, at, area, actions }: { project: string; agent: T.Agent; finishedAt?: string; at: Spot; area: RefObject<HTMLDivElement | null>; actions: Actions }) {
  const self = useRef<HTMLDivElement>(null);
  const drag = useDrag(area, self, {
    onClick: () => actions.grow(agent.ref),
    onDrop: (to) => setDock(project, (d) => ({ floats: { ...d.floats, [agent.ref]: to } })),
  });
  const p = drag.at ?? at;
  return (
    <div ref={self} className="pointer-events-auto absolute z-30 animate-pop-in" style={{ left: p.x, top: p.y, width: TILE }} {...drag.handlers} data-pip-float={agent.name}>
      <Tile project={project} agent={agent} finishedAt={finishedAt} actions={actions} onDock={() => setDock(project, (d) => ({ floats: unfloat(d, agent.ref) }))} />
    </div>
  );
}

// Tile is one small live desktop: who, and what to do with it on hover.
function Tile({ project, agent, finishedAt, paused, actions, onDock }: { project: string; agent: T.Agent; finishedAt?: string; paused?: boolean; actions: Actions; onDock?: () => void }) {
  const t = useT();
  return (
    <div className="group relative cursor-grab overflow-hidden rounded-xl border border-line-strong bg-stage shadow-[0_18px_40px_-18px_rgb(0_0_0/0.8)] active:cursor-grabbing">
      <LiveDesktop agent={agent} small paused={paused} />
      <div className="absolute inset-x-0 bottom-0 flex items-center gap-1.5 bg-gradient-to-t from-black/85 via-black/50 to-transparent px-2 pb-1.5 pt-5 text-[11px] text-white">
        <span className={cn('size-1.5 shrink-0 rounded-full', finishedAt ? 'bg-amber-300' : 'animate-pulse bg-emerald-400')} />
        <span className="shrink-0 font-medium">{agent.name}</span>
        <span className="min-w-0 truncate text-white/70">{agent.title}</span>
      </div>
      <div className="absolute right-1 top-1 flex gap-0.5 opacity-0 transition group-hover:opacity-100">
        {onDock && (
          <TileButton label={t('pip.toDock')} onClick={onDock}>
            <Minimize2 />
          </TileButton>
        )}
        <TileButton label={t('pip.grow')} onClick={() => actions.grow(agent.ref)}>
          <Maximize2 />
        </TileButton>
        <TileButton label={t('pip.pin')} onClick={() => actions.pin(agent.ref)}>
          <PanelRightOpen />
        </TileButton>
      </div>
      {finishedAt && <Finished project={project} agent={agent} />}
    </div>
  );
}

// Finished covers a tile whose agent is done: its review is a click away.
function Finished({ project, agent }: { project: string; agent: T.Agent }) {
  const t = useT();
  const openReview = useOpenReview();
  const review = useProjectReview(project);
  const item = review.data?.items?.find((i) => i.ref === agent.ref);
  return (
    <div className="absolute inset-0 flex flex-col items-center justify-center gap-1.5 bg-black/55 text-[12px] text-white backdrop-blur-[1px]" data-pip-finished={agent.name}>
      <span className="font-medium">{t('pip.finished')}</span>
      {openReview && item && (
        <button type="button" onClick={() => openReview({ project, item })} className="rounded-md bg-brand-500 px-2.5 py-1 font-medium hover:bg-brand-400" data-pip-review={agent.name}>
          {t('pip.review')}
        </button>
      )}
    </div>
  );
}

function TileButton({ label, onClick, children }: { label: string; onClick: () => void; children: ReactNode }) {
  return (
    <Tip label={label}>
      <button type="button" aria-label={label} onClick={onClick} className="flex size-6 items-center justify-center rounded-md bg-black/60 text-white/90 backdrop-blur hover:bg-black/80 [&_svg]:size-3.5">
        {children}
      </button>
    </Tip>
  );
}

// Big is a desktop grown over the chat, still floating: moved by its title,
// pinned beside the chat, or back to the dock.
function Big({ project, agent, finishedAt, area, actions }: { project: string; agent: T.Agent; finishedAt?: string; area: RefObject<HTMLDivElement | null>; actions: Actions }) {
  const t = useT();
  const self = useRef<HTMLDivElement>(null);
  const [at, setAt] = useState<Spot | null>(null);
  const drag = useDrag(area, self, { onDrop: setAt });
  const width = Math.min(720, (area.current?.clientWidth ?? 1200) * 0.6);
  const p = drag.at ?? at;
  const shrink = () => setDock(project, () => ({ big: null }));
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && !document.querySelector('[role=dialog]') && shrink();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });
  return (
    <div
      ref={self}
      className="pointer-events-auto absolute z-30 animate-pop-in overflow-hidden rounded-2xl border border-line-strong bg-modal shadow-[0_40px_120px_-30px_var(--ab-shadow-deep)]"
      style={p ? { left: p.x, top: p.y, width } : { right: TILE + 32, top: 12, width }}
      data-pip-big={agent.name}
    >
      <div className="flex cursor-grab items-center gap-2 border-b border-line py-1.5 pl-3 pr-1.5 active:cursor-grabbing" {...drag.handlers}>
        <span className={cn('size-2 shrink-0 rounded-full', finishedAt ? 'bg-amber-300' : 'animate-pulse bg-emerald-400')} />
        <span className="shrink-0 text-[13px] font-semibold text-title">{agent.name}</span>
        <span className="min-w-0 flex-1 truncate text-[12.5px] text-muted">{agent.title}</span>
        {!finishedAt && <span className="shrink-0 rounded bg-rose-500/15 px-1.5 py-px text-[10px] font-semibold tracking-wide text-rose-300">{t('pip.liveBadge')}</span>}
        <Tip label={t('pip.openAgent')}>
          <Button size="icon-sm" variant="ghost" aria-label={t('pip.openAgent')} onClick={() => actions.open(agent.ref)}>
            <ArrowUpRight />
          </Button>
        </Tip>
        <Tip label={t('pip.pin')}>
          <Button size="icon-sm" variant="ghost" aria-label={t('pip.pin')} onClick={() => actions.pin(agent.ref)} data-pip-pin>
            <PanelRightOpen />
          </Button>
        </Tip>
        <Tip label={t('pip.toDock')}>
          <Button size="icon-sm" variant="ghost" aria-label={t('pip.toDock')} onClick={shrink} data-pip-shrink>
            <Minimize2 />
          </Button>
        </Tip>
      </div>
      <div className="relative">
        <LiveDesktop agent={agent} />
        {finishedAt && <Finished project={project} agent={agent} />}
      </div>
    </div>
  );
}

// useDrag moves an element over area with the pointer, from where it is,
// telling a click from a drag; buttons in it stay buttons.
function useDrag(area: RefObject<HTMLDivElement | null>, self: RefObject<HTMLDivElement | null>, { onClick, onDrop }: { onClick?: () => void; onDrop: (at: Spot) => void }) {
  const [at, setAt] = useState<Spot | null>(null);
  const start = useRef<{ px: number; py: number; ox: number; oy: number; moved: boolean } | null>(null);
  const handlers = {
    onPointerDown: (e: ReactPointerEvent<HTMLElement>) => {
      if (e.button !== 0 || (e.target as HTMLElement).closest('button')) return;
      const box = area.current?.getBoundingClientRect();
      const r = self.current?.getBoundingClientRect();
      if (!box || !r) return;
      e.currentTarget.setPointerCapture(e.pointerId);
      start.current = { px: e.clientX, py: e.clientY, ox: r.left - box.left, oy: r.top - box.top, moved: false };
    },
    onPointerMove: (e: ReactPointerEvent<HTMLElement>) => {
      const s = start.current;
      const box = area.current;
      const el = self.current;
      if (!s || !box || !el) return;
      const dx = e.clientX - s.px;
      const dy = e.clientY - s.py;
      if (!s.moved && Math.hypot(dx, dy) < 5) return;
      s.moved = true;
      setAt(clampSpot({ x: s.ox + dx, y: s.oy + dy }, el.offsetWidth, Math.min(el.offsetHeight, 160), box.clientWidth, box.clientHeight));
    },
    onPointerUp: () => {
      const s = start.current;
      start.current = null;
      if (!s) return;
      if (!s.moved) onClick?.();
      else if (at) onDrop(at);
      setAt(null);
    },
    onPointerCancel: () => {
      start.current = null;
      setAt(null);
    },
  };
  return { at, handlers };
}

function useSize(ref: RefObject<HTMLElement | null>) {
  const [size, setSize] = useState({ width: 0, height: 0 });
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => setSize({ width: el.clientWidth, height: el.clientHeight });
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    measure();
    return () => ro.disconnect();
  }, [ref]);
  return size;
}

// useMinute is the time, a minute at a time: when a finished agent's tile
// has had its while.
function useMinute() {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 60_000);
    return () => window.clearInterval(id);
  }, []);
  return now;
}
