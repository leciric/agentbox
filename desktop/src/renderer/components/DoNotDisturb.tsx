import { useT, type Translate } from '../lib/i18n';
import { forever, setDnd, useDnd, type Duration } from '../lib/notifyPrefs';
import { cn } from '../lib/utils';
import { Button } from './ui/button';

export const durations: readonly Duration[] = ['hour', 'tomorrow', 'forever'];

// untilText says when it ends: a time of day, with the weekday if it isn't today.
export function untilText(until: number, t: Translate, now = Date.now()): string {
  if (until === forever) return t('notify.dnd.untilOff');
  const d = new Date(until);
  const time = d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  const today = new Date(now).toDateString() === d.toDateString();
  return t('notify.dnd.until', { when: today ? time : `${d.toLocaleDateString([], { weekday: 'short' })} ${time}` });
}

// DoNotDisturbControl is the durations that turn it on and the way to turn it
// off, in Settings → Notifications: silencing the sounds and system
// notifications (lib/notifyPrefs.ts) for a while. The inbox says when it's on.
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
