import { useQuery } from '@tanstack/react-query';
import { ArrowUpRight, ChevronLeft, ChevronRight, Minimize2, PanelRightOpen } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from 'react';
import * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { docked } from '../../lib/pip';
import { setLane, useLane } from '../../lib/pipStore';
import { openPanelOn, useChatPanel } from '../../lib/rightPanel';
import { cn } from '../../lib/utils';
import { Button } from '../ui/button';
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

// The lane's widths: a column of small tiles, or one desktop grown in it.
const small = 136;
const grownMin = 280;
const grownMax = 440;
// foldBelow is how wide the chat's box must be for the lane to open by
// itself: narrower, the chat keeps the room and the lane is a strip.
const foldBelow = 760;

// PipLane is the lead's chat with a lane at its right for the desktops its
// agents are using: small live tiles, one of them grown on a click. The
// chat, composer and all, gives the lane its room rather than lying under it;
// a desktop pinned beside the chat is the right-hand panel's Desktop tab.
export function PipLane({ project, onSelect, children }: { project: string; onSelect: (view: View) => void; children: ReactNode }) {
  const t = useT();
  const row = useRef<HTMLDivElement>(null);
  const width = useWidth(row);
  const lane = useLane(project);
  const chat = `${project}/${T.LeadName}`;
  const panel = useChatPanel(chat);
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const inUse = useInUse(project);
  // The agent whose desktop is open in the panel beside the chat has no tile.
  const pinned = panel.open ? panel.tabs.find((tab) => tab.id === panel.active && tab.kind === 'desktop')?.target : undefined;
  const tiles = inUse.filter((a) => a.ref !== pinned);
  // A desktop you grew stays while its agent runs, in use or not: you're watching it.
  const big = lane.big && lane.big !== pinned ? (agents.data ?? []).find((a) => a.ref === lane.big && a.state === 'running') : undefined;
  const others = tiles.filter((a) => a.ref !== big?.ref);
  const folded = lane.folded ?? width < foldBelow;
  const laneWidth = big ? Math.max(grownMin, Math.min(grownMax, Math.round(width * 0.36))) : small;

  const shrink = () => setLane(project, () => ({ big: null }));
  const pin = (ref: string) => {
    setLane(project, (l) => ({ big: l.big === ref ? null : l.big }));
    openPanelOn(chat, 'desktop', ref);
  };
  useEffect(() => {
    if (!big) return;
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && !document.querySelector('[role=dialog]') && setLane(project, () => ({ big: null }));
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [big, project]);

  return (
    <div ref={row} className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">{children}</div>
      {(big || others.length > 0) &&
        (folded ? (
          <Strip count={tiles.length} onOpen={() => setLane(project, () => ({ folded: false }))} />
        ) : (
          <aside className="flex shrink-0 flex-col border-l border-line transition-[width] duration-200" style={{ width: laneWidth }} data-pip-lane={big ? 'grown' : 'tiles'}>
            <div className="flex h-8 shrink-0 items-center gap-1.5 pl-3 pr-1 text-[11px] text-subtle">
              <span className={cn('size-1.5 rounded-full', tiles.length > 0 ? 'animate-pulse bg-rose-400' : 'bg-faint')} />
              <span className="flex-1 truncate">{t('pip.live', { count: tiles.length })}</span>
              <Tip label={t('pip.hide')}>
                <button type="button" aria-label={t('pip.hide')} onClick={() => setLane(project, () => ({ folded: true, big: null }))} className="rounded p-1 hover:bg-surface-raised hover:text-primary" data-pip-fold>
                  <ChevronRight className="size-3.5" />
                </button>
              </Tip>
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2 [scrollbar-width:thin]">
              {big && <Grown agent={big} inUse={inUse.some((a) => a.ref === big.ref)} onPin={() => pin(big.ref)} onOpen={() => onSelect({ kind: 'agent', ref: big.ref })} onShrink={shrink} />}
              <div className={cn('grid gap-2.5', big ? 'mt-3 grid-cols-[repeat(auto-fill,minmax(92px,1fr))]' : 'grid-cols-1')}>
                {others.map((a) => (
                  <Tile key={a.ref} agent={a} onClick={() => setLane(project, () => ({ big: a.ref }))} />
                ))}
              </div>
            </div>
          </aside>
        ))}
    </div>
  );
}

// Tile is one small live desktop, and whose it is under it.
function Tile({ agent, onClick }: { agent: T.Agent; onClick: () => void }) {
  return (
    <Tip label={agent.title || agent.name} side="left">
      <button type="button" onClick={onClick} className="group block w-full min-w-0 text-left" data-pip-tile={agent.name}>
        <div className="overflow-hidden rounded-lg border border-line-strong bg-stage transition group-hover:border-line-heavy">
          <LiveDesktop agent={agent} small />
        </div>
        <div className="mt-1 flex min-w-0 items-center gap-1 px-0.5 text-[10.5px] text-muted group-hover:text-primary">
          <span className="size-1.5 shrink-0 animate-pulse rounded-full bg-emerald-400" />
          <span className="truncate">{agent.name}</span>
        </div>
      </button>
    </Tip>
  );
}

// Grown is one desktop at the lane's full width: pinned beside the chat,
// opened on its agent's page, or put back with the others.
function Grown({ agent, inUse, onPin, onOpen, onShrink }: { agent: T.Agent; inUse: boolean; onPin: () => void; onOpen: () => void; onShrink: () => void }) {
  const t = useT();
  return (
    <div className="animate-fade-in" data-pip-big={agent.name}>
      <div className="flex min-w-0 items-center gap-1 pb-1.5 pl-1">
        <span className={cn('size-1.5 shrink-0 rounded-full', inUse ? 'animate-pulse bg-emerald-400' : 'bg-faint')} />
        <span className="shrink-0 text-[12.5px] font-semibold text-title">{agent.name}</span>
        <span className="min-w-0 flex-1 truncate pl-0.5 text-[12px] text-muted">{agent.title}</span>
        <LaneButton label={t('pip.openAgent')} onClick={onOpen}>
          <ArrowUpRight />
        </LaneButton>
        <LaneButton label={t('pip.pin')} onClick={onPin} data="pin">
          <PanelRightOpen />
        </LaneButton>
        <LaneButton label={t('pip.toDock')} onClick={onShrink} data="shrink">
          <Minimize2 />
        </LaneButton>
      </div>
      <div className="overflow-hidden rounded-lg border border-line-strong bg-stage">
        <LiveDesktop agent={agent} />
      </div>
      {!inUse && <p className="mt-1.5 px-1 text-[11px] text-subtle">{t('pip.idle')}</p>}
    </div>
  );
}

function LaneButton({ label, onClick, data, children }: { label: string; onClick: () => void; data?: string; children: ReactNode }) {
  return (
    <Tip label={label}>
      <Button size="icon-sm" variant="ghost" className="size-7 shrink-0 [&_svg]:size-3.5" aria-label={label} onClick={onClick} data-pip-action={data}>
        {children}
      </Button>
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
