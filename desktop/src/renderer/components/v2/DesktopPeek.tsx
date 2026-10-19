// An agent's computer at a glance: the glyph on its sidebar row, and the
// frame its hover card and page lead with. Each agent has a machine of its
// own, and these are where that shows before you open anything: its desktop
// live while it is up, else the last frame it captured, and in a word what
// the machine is doing (lib/rightPanel.ts's deskOf).
import { useQuery } from '@tanstack/react-query';
import { Monitor, MonitorPause, MonitorPlay } from 'lucide-react';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { useT, type MessageKey } from '../../lib/i18n';
import { mediaUrl } from '../../lib/media';
import { mediaQuery } from '../../lib/mediaPages';
import { deskOf, type Desk } from '../../lib/rightPanel';
import { cn, timeAgo } from '../../lib/utils';
import { LiveDesktop } from '../pip/LiveDesktop';
import { useInUse } from '../pip/inUse';
import { standIn } from '../pip/standIn';

export const deskLabel: Record<Desk, MessageKey> = {
  live: 'desk.live',
  on: 'desk.on',
  asleep: 'desk.asleep',
};

// useDesk is what agent's computer is doing.
export function useDesk(agent: T.Agent): Desk {
  const inUse = useInUse(agent.project).some((a) => a.ref === agent.ref);
  return deskOf(agent, inUse);
}

// DeskGlyph is the computer on an agent's row: playing while its desktop is
// in use, quiet while it's on, paused while it sleeps.
export function DeskGlyph({ desk, className }: { desk: Desk; className?: string }) {
  const t = useT();
  const Icon = desk === 'live' ? MonitorPlay : desk === 'asleep' ? MonitorPause : Monitor;
  return (
    <span className={cn('relative inline-flex shrink-0', className)} role="img" aria-label={t(deskLabel[desk])} data-desk={desk}>
      <Icon className={cn('size-3.5', desk === 'live' ? 'text-emerald-400' : 'text-faint')} />
      {desk === 'live' && <span className="absolute -right-0.5 -top-0.5 size-1.5 animate-pulse rounded-full bg-emerald-400" />}
    </span>
  );
}

// useDeskUp is whether agent's desktop is up: its display running, which
// a machine that is on may not have started yet.
export function useDeskUp(agent: T.Agent): boolean {
  const running = agent.state === 'running';
  const status = useQuery({ queryKey: ['browser', agent.ref], queryFn: () => api.browser(agent.ref), enabled: running && !standIn, staleTime: 10_000 });
  return standIn ? running : running && status.data?.display === true;
}

// deskLine is what the machine is doing, in a line: an "on" machine says
// whether its desktop is idle or not started.
export const deskLine = (desk: Desk, up: boolean): MessageKey => (desk === 'on' ? (up ? 'desk.idle' : 'desk.noDesktop') : deskLabel[desk]);

// useLastFrame is the latest screenshot agent took, its desktop's last frame.
export function useLastFrame(agent: Pick<T.Agent, 'project' | 'name'>, enabled = true) {
  return useQuery({
    queryKey: ['lastFrame', agent.project, agent.name],
    queryFn: async () => {
      const q = mediaQuery({ project: agent.project, agent: agent.name, kinds: ['screenshot'] });
      q.set('limit', '1');
      return (await api.mediaPage(q)).items?.[0] ?? null;
    },
    enabled,
    staleTime: 30_000,
  });
}

// DesktopPeek is the agent's desktop, at 16:10: live while it's up, else its
// last frame and when, else that there is nothing to show yet; under it, what
// the machine is doing.
export function DesktopPeek({ agent, desk, className, caption = true }: { agent: T.Agent; desk: Desk; className?: string; caption?: boolean }) {
  const t = useT();
  const up = useDeskUp(agent);
  const last = useLastFrame(agent, !up);
  const frame = last.data;
  return (
    <div className={cn('grid gap-1.5', className)} data-desktop-peek={agent.ref}>
      <div className="relative overflow-hidden rounded-lg border border-line-strong bg-stage">
        {up ? (
          <LiveDesktop agent={agent} small />
        ) : frame ? (
          <>
            <img src={mediaUrl(frame)} alt="" className="aspect-[16/10] w-full object-cover object-top" />
            <span className="absolute bottom-1 left-1 rounded bg-black/65 px-1.5 py-px text-[10px] text-white">{t('desk.lastFrame', { ago: timeAgo(frame.createdAt) })}</span>
          </>
        ) : (
          <div className="flex aspect-[16/10] w-full flex-col items-center justify-center gap-1 text-[11px] text-subtle">
            <Monitor className="size-4 text-faint" />
            {last.isPending ? t('common.loading') : t('desk.nothing')}
          </div>
        )}
        {up && <span className="absolute left-1 top-1 rounded bg-rose-500/90 px-1 text-[9px] font-bold tracking-wider text-white">{t('panel.live')}</span>}
      </div>
      {caption && (
        <span className="flex min-w-0 items-center gap-1.5 text-[11px] text-secondary">
          <DeskGlyph desk={desk} />
          <span className="truncate">{t(deskLine(desk, up))}</span>
        </span>
      )}
    </div>
  );
}
