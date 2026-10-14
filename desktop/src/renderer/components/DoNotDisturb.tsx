import { BellOff, Moon } from 'lucide-react';
import { useT, type Translate } from '../lib/i18n';
import { forever, setDnd, useDnd, type Duration } from '../lib/notifyPrefs';
import { cn } from '../lib/utils';
import { Button } from './ui/button';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';

export const durations: readonly Duration[] = ['hour', 'tomorrow', 'forever'];

// DoNotDisturb is the top bar's way to silence the sounds and system
// notifications (lib/notifyPrefs.ts) for a while: a moon that becomes an
// amber crossed-out bell while it's on.
export function DoNotDisturb() {
  const t = useT();
  const { on } = useDnd();
  const Icon = on ? BellOff : Moon;
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          aria-label={on ? t('notify.dnd.on') : t('notify.dnd.title')}
          title={on ? t('notify.dnd.on') : t('notify.dnd.title')}
          data-dnd-button
          data-dnd={on ? 'on' : 'off'}
          className={cn(
            'flex size-8 shrink-0 items-center justify-center rounded-full border transition',
            on
              ? 'border-amber-400/30 bg-amber-400/10 text-amber-200 hover:bg-amber-400/15 [:root[data-appearance=light]_&]:text-amber-700'
              : 'border-line bg-surface-faint text-muted hover:bg-surface-raised hover:text-primary',
          )}
        >
          <Icon className="size-4" />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-64 p-3" align="end">
        <DoNotDisturbControl />
      </PopoverContent>
    </Popover>
  );
}

// untilText says when it ends: a time of day, with the weekday if it isn't today.
export function untilText(until: number, t: Translate, now = Date.now()): string {
  if (until === forever) return t('notify.dnd.untilOff');
  const d = new Date(until);
  const time = d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  const today = new Date(now).toDateString() === d.toDateString();
  return t('notify.dnd.until', { when: today ? time : `${d.toLocaleDateString([], { weekday: 'short' })} ${time}` });
}

// DoNotDisturbControl is the durations that turn it on and the way to turn it
// off, in Settings and in the top bar's popover.
export function DoNotDisturbControl({ className, titled = true }: { className?: string; titled?: boolean }) {
  const t = useT();
  const { on, until } = useDnd();
  return (
    <div className={cn('grid gap-2', className)} data-dnd-control>
      {titled && <div className="text-[13px] font-medium text-title">{t('notify.dnd.title')}</div>}
      {(on || titled) && (
        <p className="text-[12px] text-muted" data-dnd-status>
          {on ? untilText(until, t) : t('notify.dnd.hint')}
        </p>
      )}
      <div className="flex flex-wrap gap-1.5" role="group" aria-label={t('notify.dnd.title')}>
        {durations.map((d) => (
          <Button key={d} size="sm" variant="secondary" data-dnd-duration={d} onClick={() => setDnd(d)}>
            {t(`notify.dnd.for.${d}`)}
          </Button>
        ))}
        {on && (
          <Button size="sm" variant="ghost" data-dnd-off onClick={() => setDnd(null)}>
            {t('notify.dnd.turnOff')}
          </Button>
        )}
      </div>
    </div>
  );
}
