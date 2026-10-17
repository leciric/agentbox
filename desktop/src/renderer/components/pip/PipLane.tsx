import { useQuery } from '@tanstack/react-query';
import { ChevronLeft, ChevronRight, PanelRightOpen } from 'lucide-react';
import { useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from 'react';
import * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { docked } from '../../lib/pip';
import { setLane, useLane } from '../../lib/pipStore';
import { openPanelOn, useChatPanel } from '../../lib/rightPanel';
import { Tip } from '../ui/tooltip';
import { LiveDesktop } from './LiveDesktop';

// useDesktops is when each of a project's agents last used its desktop, asked
// again every few seconds while a lead's chat shows it.
export function useDesktops(project: string) {
  return useQuery({ queryKey: ['desktops', project], queryFn: () => api.projectDesktops(project), refetchInterval: 5_000 });
}

// useInUse is the project's agents using their desktops now (lib/pip.ts).
export function useInUse(project: string): T.Agent[] {
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const desktops = useDesktops(project);
  return docked(project, agents.data ?? [], desktops.data ?? [], Date.now());
}

// The lane's width to begin with, a column of small tiles each named by its
// task, and the narrowest it is dragged to. It never takes the chat's room
// below chatMin; its tiles grow with it, to fill their column at the desktop's 16:10, and go
// two across once each could stay at least tileMin wide, so a wider lane
// always means bigger desktops, never just more room around them.
const laneWidth = 168;
const laneMin = laneWidth;
const chatMin = 420;
const tileMin = 200;

// laneBox is the lane's width kept within what the chat can spare of room.
export function laneBox(want: number | undefined, room: number): number {
  const max = Math.max(laneMin, room - chatMin);
  return Math.min(Math.max(want ?? laneWidth, laneMin), max);
}

// laneColumns is how many tiles go across a lane of this width.
export function laneColumns(width: number): number {
  return Math.max(1, Math.floor((width - 16 + 12) / (tileMin + 12)));
}
// foldBelow is how wide the chat's box must be for the lane to open by
// itself: narrower, the chat keeps the room and the lane is a strip.
const foldBelow = 760;

// PipLane is the lead's chat with a lane at its right for the desktops its
// agents are using: small live tiles, each named by its agent's task. A tile
// opens that desktop in the right-hand panel, the one place a desktop is
// shown large, and the lane steps aside while the panel is open: the chat is
// never squeezed between the two, and the panel's picker marks who is live.
// The chat, composer and all, gives the lane its room rather than lying
// under it.
export function PipLane({ project, children }: { project: string; onSelect?: (view: View) => void; children: ReactNode }) {
  const row = useRef<HTMLDivElement>(null);
  const width = useWidth(row);
  const lane = useLane(project);
  const chat = `${project}/${T.LeadName}`;
  const open = useChatPanel(chat).open;
  const tiles = useInUse(project);
  const folded = lane.folded ?? width < foldBelow;
  const box = laneBox(lane.width, width);

  return (
    <div ref={row} className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">{children}</div>
      {!open && tiles.length > 0 && (folded ? <Strip count={tiles.length} onOpen={() => setLane(project, () => ({ folded: false }))} /> : <Lane project={project} tiles={tiles} width={box} onOpen={(ref) => openPanelOn(chat, 'desktop', ref)} />)}
    </div>
  );
}

function Lane({ project, tiles, width, onOpen }: { project: string; tiles: T.Agent[]; width: number; onOpen: (ref: string) => void }) {
  const t = useT();
  return (
    <aside className="relative flex shrink-0 flex-col border-l border-line" style={{ width }} data-pip-lane="tiles">
      <WidthHandle project={project} width={width} />
      <div className="flex h-8 shrink-0 items-center gap-1.5 pl-3 pr-1 text-[11px] text-subtle">
        <span className="size-1.5 animate-pulse rounded-full bg-rose-400" />
        <span className="flex-1 truncate">{t('pip.live', { count: tiles.length })}</span>
        <Tip label={t('pip.hide')}>
          <button type="button" aria-label={t('pip.hide')} onClick={() => setLane(project, () => ({ folded: true }))} className="rounded p-1 hover:bg-surface-raised hover:text-primary" data-pip-fold>
            <ChevronRight className="size-3.5" />
          </button>
        </Tip>
      </div>
      <div className="grid min-h-0 flex-1 content-start gap-3 overflow-y-auto px-2 pb-2 [scrollbar-width:thin]" style={{ gridTemplateColumns: `repeat(${laneColumns(width)}, minmax(0, 1fr))` }}>
        {tiles.map((a) => (
          <Tile key={a.ref} agent={a} onClick={() => onOpen(a.ref)} />
        ))}
      </div>
    </aside>
  );
}

// WidthHandle drags the lane's left edge, like the panel's; a double click
// puts it back. The most it takes is checked when it is drawn (laneBox).
function WidthHandle({ project, width }: { project: string; width: number }) {
  const t = useT();
  const last = useRef<number | null>(null);
  const w = useRef(width);
  w.current = width;
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={t('pip.resize')}
      className="absolute inset-y-0 left-0 z-10 w-1.5 -translate-x-1/2 cursor-col-resize transition-colors hover:bg-brand-400/30 active:bg-brand-400/40"
      onPointerDown={(event) => {
        event.preventDefault();
        event.currentTarget.setPointerCapture(event.pointerId);
        last.current = event.clientX;
      }}
      onPointerMove={(event) => {
        if (last.current === null) return;
        const dx = event.clientX - last.current;
        if (!dx) return;
        last.current = event.clientX;
        setLane(project, () => ({ width: Math.max(laneMin, w.current - dx) }));
      }}
      onPointerUp={() => (last.current = null)}
      onPointerCancel={() => (last.current = null)}
      onDoubleClick={() => setLane(project, () => ({ width: undefined }))}
      data-pip-splitter
    />
  );
}

// Tile is one small live desktop, and under it whose: its task, as the
// sidebar names it, and its number. A click opens it in the panel.
function Tile({ agent, onClick }: { agent: T.Agent; onClick: () => void }) {
  const t = useT();
  return (
    <Tip label={t('pip.open', { name: agent.title || agent.name })} side="left">
      <button type="button" onClick={onClick} className="group block w-full min-w-0 text-left" data-pip-tile={agent.name}>
        <div className="relative overflow-hidden rounded-lg border border-line-strong bg-stage transition group-hover:border-brand-500/60">
          <LiveDesktop agent={agent} small />
          <span className="absolute inset-0 flex items-center justify-center bg-black/40 text-white opacity-0 transition group-hover:opacity-100">
            <PanelRightOpen className="size-4" />
          </span>
        </div>
        <div className="mt-1 flex min-w-0 items-center gap-1 px-0.5 text-[11px] font-medium text-secondary group-hover:text-title">
          <span className="mt-[5px] size-1.5 shrink-0 self-start animate-pulse rounded-full bg-emerald-400" />
          <span className="line-clamp-2 leading-snug">{agent.title || agent.name}</span>
        </div>
        {agent.title && <div className="truncate px-0.5 pl-3 font-mono text-[10px] text-faint">{agent.name}</div>}
      </button>
    </Tip>
  );
}

// Strip is the lane folded: how many desktops are in use, a click from open.
function Strip({ count, onOpen }: { count: number; onOpen: () => void }) {
  const t = useT();
  return (
    <Tip label={t('pip.show')} side="left">
      <button type="button" onClick={onOpen} className="flex w-7 shrink-0 flex-col items-center gap-1.5 border-l border-line pt-2.5 text-[11px] text-subtle hover:bg-surface hover:text-primary" data-pip-lane="folded">
        <ChevronLeft className="size-3.5" />
        <span className="size-1.5 animate-pulse rounded-full bg-rose-400" />
        <span className="tabular-nums">{count}</span>
      </button>
    </Tip>
  );
}

function useWidth(ref: RefObject<HTMLElement | null>) {
  const [width, setWidth] = useState(0);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setWidth(el.clientWidth));
    ro.observe(el);
    setWidth(el.clientWidth);
    return () => ro.disconnect();
  }, [ref]);
  return width;
}
