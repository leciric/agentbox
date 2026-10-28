import { AlarmClock, ExternalLink, Zap } from 'lucide-react';
import type * as T from '../../../shared/api';
import { formatDateTime, useT } from '../../lib/i18n';
import { leadOf, openPanelOn } from '../../lib/rightPanel';
import { cn } from '../../lib/utils';
import { Markdown } from './Markdown';

// ScheduledRunChip is one of the project's scheduled tasks arriving in its
// lead's chat (internal/daemon/schedules.go): a small chip saying which, and
// whether it ran late or what event set it off, over the task it brought,
// with a link to the panel's Schedules. The lead itself reads the task from a hidden notice, and its
// answer follows this.
export function ScheduledRunChip({ item, project }: { item: T.ChatItem; project?: string }) {
  const t = useT();
  const run = item.scheduledRun!;
  const lines = [
    run.trigger === 'manual' && t('chat.scheduled.manual'),
    run.event,
    run.late && run.dueAt && t('chat.scheduled.late', { due: formatDateTime(run.dueAt) }),
    run.late && (run.missed ?? 0) > 0 && t('chat.scheduled.missed', { count: run.missed ?? 0 }),
    run.target === 'agent' && (run.agent ? t('chat.scheduled.toAgent', { agent: run.agent }) : t('chat.scheduled.toNewAgent')),
  ].filter(Boolean);
  return (
    <div className="mb-4 min-w-0 text-[13px]" data-chat-item="scheduled-run" data-schedule={run.schedule} data-chat-id={item.id}>
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <span
          className={cn(
            'inline-flex min-w-0 items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-[12px] font-medium',
            run.late ? 'border-amber-500/30 bg-amber-500/[0.08] text-amber-200' : 'border-sky-500/30 bg-sky-500/[0.08] text-sky-200',
          )}
          data-scheduled-chip
        >
          {run.trigger === 'event' ? <Zap className="size-3.5 shrink-0" /> : <AlarmClock className="size-3.5 shrink-0" />}
          <span className="truncate">{t(run.trigger === 'event' ? 'chat.scheduled.eventTitle' : 'chat.scheduled.title', { title: run.title })}</span>
        </span>
        {lines.length > 0 && <span className="min-w-0 truncate text-[12px] text-subtle">{lines.join(' · ')}</span>}
        {project && (
          <button
            type="button"
            className="ml-auto flex shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-[12px] text-subtle transition hover:bg-surface-strong hover:text-primary"
            onClick={() => openPanelOn(leadOf(project), 'schedules')}
            data-open-schedules
          >
            {t('chat.scheduled.open')}
            <ExternalLink className="size-3.5" />
          </button>
        )}
      </div>
      <div className="mt-1.5 min-w-0 break-words border-l-2 border-line-faint pl-3 text-secondary" data-chat-text>
        <Markdown text={item.text ?? ''} />
      </div>
    </div>
  );
}
