import { useQuery } from '@tanstack/react-query';
import { Play } from 'lucide-react';
import type { View } from '../App';
import { api } from '../lib/api';
import { useConnection } from '../lib/events';
import { formatTime, useT } from '../lib/i18n';
import { cn, humanBytes } from '../lib/utils';
import type * as T from '../../shared/api';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';

// RunSlotsPill is the VM's run slots in the top bar: "Run slots 2 of 3 free".
// An agent's run phase (the app, docker compose, Playwright, its desktop,
// browser or emulator, recordings) takes one, and waits in line while none
// is free; coding never waits. The popover lists who holds a slot, who waits
// and how much each project's run is counted as. Amber while agents wait.
// Nothing while no agent runs, or from a daemon without run slots.
export function RunSlotsPill({ agents, onSelect }: { agents: T.Agent[]; onSelect: (view: View) => void }) {
  const t = useT();
  const connection = useConnection();
  // Every change is pushed as an event (lib/events.ts); the poll is for what
  // the holders use, which moves without one.
  const slots = useQuery({
    queryKey: ['runSlots'],
    queryFn: api.runSlots,
    refetchInterval: 10_000,
    retry: false,
    enabled: connection.state === 'connected',
  });
  const data = slots.data;
  const running = agents.some((a) => a.state === 'running');
  if (!data || (!running && data.inUse === 0 && data.queued === 0)) return null;
  const waiting = data.queued > 0;
  const label = t('shell.runSlots.label', {
    free: data.free,
    total: data.total,
  });
  const titleOf = (ref: string) => {
    const agent = agents.find((a) => a.ref === ref);
    return agent?.title ? `${agent.title} · ${ref}` : ref;
  };
  const open = (ref: string) => onSelect({ kind: 'agent', ref });
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className={cn(
            'flex items-center gap-1.5 rounded-full px-2.5 py-1 text-caption transition bg-surface-raised',
            waiting ? 'bg-amber-400/10 text-amber-200 hover:bg-amber-400/15' : 'text-secondary hover:bg-surface-strong',
          )}
          aria-label={label}
          data-run-slots={`${data.free}/${data.total}`}
          data-run-slots-queued={data.queued}
        >
          <Play className="size-3.5" />
          <span className="hidden whitespace-nowrap xl:inline">{label}</span>
          <span className="whitespace-nowrap tabular-nums xl:hidden">{t('shell.runSlots.short', { free: data.free, total: data.total })}</span>
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-96">
        <div className="grid gap-3" data-run-slots-popover>
          <div className="grid gap-1">
            <span className={cn('text-ui font-medium', waiting ? 'text-amber-200' : 'text-primary')}>
              {t('shell.runSlots.title', {
                free: data.free,
                total: data.total,
              })}
            </span>
            <span className="text-caption leading-relaxed text-secondary">{t('shell.runSlots.explain')}</span>
          </div>
          <Section title={t('shell.runSlots.holders')}>
            {data.holders.length === 0 && <span className="text-caption text-muted">{t('shell.runSlots.nobody')}</span>}
            {data.holders.map((h) => (
              <Row
                key={h.agent}
                name={titleOf(h.agent)}
                onClick={() => open(h.agent)}
                detail={t('shell.runSlots.held', {
                  since: formatTime(h.since),
                  until: formatTime(h.expires),
                })}
                side={t('shell.runSlots.use', {
                  use: humanBytes(h.use),
                  reserved: humanBytes(h.reserved),
                })}
              />
            ))}
          </Section>
          {data.queue.length > 0 && (
            <Section title={t('shell.runSlots.queue')}>
              {data.queue.map((w) => (
                <Row
                  key={w.agent}
                  name={titleOf(w.agent)}
                  onClick={() => open(w.agent)}
                  detail={t('shell.runSlots.waiting', {
                    since: formatTime(w.since),
                  })}
                  side={t('shell.runSlots.ahead', { ahead: w.ahead })}
                  tone="warn"
                />
              ))}
            </Section>
          )}
          {data.projects.length > 0 && (
            <Section title={t('shell.runSlots.projects')}>
              {data.projects.map((p) => (
                <div key={p.project} className="flex items-baseline justify-between gap-3 text-caption" data-run-peak={p.project}>
                  <span className="min-w-0 truncate text-secondary">
                    <span className="text-primary">{p.project}</span>{' '}
                    {p.learned
                      ? t('shell.runSlots.peakLearned', {
                          peak: humanBytes(p.peak),
                          runs: p.runs,
                        })
                      : t('shell.runSlots.peakDefault', {
                          peak: humanBytes(p.peak),
                        })}
                  </span>
                  <span className="shrink-0 text-muted">{t('shell.runSlots.fits', { free: p.free })}</span>
                </div>
              ))}
            </Section>
          )}
          {data.pool > 0 && (
            <span className="border-t border-line-faint pt-2.5 text-caption text-faint">
              {t('shell.runSlots.memory', {
                free: humanBytes(data.freeMemory),
                pool: humanBytes(data.pool),
                code: humanBytes(data.codeUse),
              })}
            </span>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-1.5">
      <span className="text-caption font-medium text-secondary">{title}</span>
      {children}
    </div>
  );
}

function Row({ name, detail, side, onClick, tone }: { name: string; detail: string; side: string; onClick: () => void; tone?: 'warn' }) {
  return (
    <button type="button" onClick={onClick} className="grid min-w-0 gap-0.5 rounded-md px-1.5 py-1 text-left transition hover:bg-surface-strong">
      <span className="flex min-w-0 items-baseline justify-between gap-3 text-caption">
        <span className="min-w-0 truncate text-primary">{name}</span>
        <span className={cn('shrink-0', tone === 'warn' ? 'text-amber-200' : 'text-muted')}>{side}</span>
      </span>
      <span className="truncate text-caption text-faint">{detail}</span>
    </button>
  );
}
