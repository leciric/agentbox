// What a session runs outside its turns (T.ChatSession.backgroundTasks): a
// chip for each in the timeline, and behind a chip its status and its output
// as it is written. A subagent's chip goes to its card instead, which is
// where everything it said and did is.
import { useQuery } from '@tanstack/react-query';
import { Bot, Check, CircleAlert, CircleSlash, Eye, Hourglass, LoaderCircle, SquareTerminal, TimerOff, Workflow } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { withEarlier, withTail, type OutputBuffer } from '../../lib/backgroundOutput';
import { formatDuration } from '../../lib/chat';
import { formatDateTime, useT, type MessageKey } from '../../lib/i18n';
import { cn } from '../../lib/utils';
import { Button } from '../ui/button';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '../ui/dialog';
import { Elapsed } from './Timeline';

const kindIcon = { command: SquareTerminal, monitor: Eye, subagent: Bot, task: Workflow } as const;
const kindKey = (kind: string) => `chat.background.kind.${kind in kindIcon ? kind : 'task'}` as MessageKey;
const statusKey = (status: string) =>
  `chat.background.status.${['running', 'done', 'failed', 'timed_out', 'stopped'].includes(status) ? status : 'stopped'}` as MessageKey;

function StatusIcon({ status, className }: { status: string; className?: string }) {
  if (status === 'running') return <LoaderCircle className={cn('size-3.5 shrink-0 animate-spin text-subtle', className)} />;
  if (status === 'done') return <Check className={cn('size-3.5 shrink-0 text-emerald-400', className)} />;
  if (status === 'failed') return <CircleAlert className={cn('size-3.5 shrink-0 text-rose-400', className)} />;
  if (status === 'timed_out') return <TimerOff className={cn('size-3.5 shrink-0 text-amber-400', className)} />;
  return <CircleSlash className={cn('size-3.5 shrink-0 text-faint', className)} />;
}

const statusTone = (status: string) =>
  status === 'failed' ? 'text-rose-300' : status === 'timed_out' ? 'text-amber-300' : status === 'done' ? 'text-emerald-300' : 'text-subtle';

// BackgroundRow is the timeline's row of them. names is what an older daemon
// says instead: descriptions only, with nothing to open.
export function BackgroundRow({
  chatRef,
  names,
  tasks,
  waiting,
  onRevealItem,
}: {
  chatRef: string;
  names: string[];
  tasks: T.ChatBackgroundTask[];
  waiting: boolean;
  onRevealItem?: (item: string) => void;
}) {
  const t = useT();
  const [open, setOpen] = useState<string>();
  const shown = tasks.find((task) => task.id === open);
  if (tasks.length === 0)
    return (
      <div className="mb-3 flex items-center gap-2 px-1 text-sm text-subtle" data-chat-background>
        <Hourglass className="size-4 shrink-0" />
        <span className="min-w-0 break-words">{t('chat.timeline.background', { tasks: names.join(', ') })}</span>
      </div>
    );
  return (
    <div className="mb-3 flex min-w-0 items-start gap-2 px-1 text-sm text-subtle" data-chat-background>
      <Hourglass className="mt-1 size-4 shrink-0" />
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
        <span className="shrink-0 py-0.5">{t(waiting ? 'chat.background.awaiting' : 'chat.background.heading')}</span>
        {tasks.map((task) => {
          const Icon = kindIcon[task.kind as keyof typeof kindIcon] ?? Workflow;
          const toCard = task.kind === 'subagent' && !!task.item && !!onRevealItem;
          return (
            <button
              key={task.id}
              className="flex min-w-0 max-w-full items-center gap-1.5 rounded-md border border-line-faint bg-surface-faint px-2 py-0.5 text-ui text-secondary transition-colors hover:border-line hover:bg-surface-raised hover:text-primary"
              title={t(toCard ? 'chat.background.openSubagent' : 'chat.background.open')}
              data-chat-background-task={task.id}
              data-status={task.status}
              onClick={() => (toCard ? onRevealItem(task.item!) : setOpen(task.id))}
            >
              <Icon className="size-3.5 shrink-0 text-subtle" strokeWidth={1.8} />
              <span className="min-w-0 truncate">{task.title}</span>
              <StatusIcon status={task.status} />
              <span className="sr-only">{t(statusKey(task.status))}</span>
            </button>
          );
        })}
      </div>
      {shown && <BackgroundTaskDialog chatRef={chatRef} task={shown} onClose={() => setOpen(undefined)} />}
    </div>
  );
}

function BackgroundTaskDialog({ chatRef, task: listed, onClose }: { chatRef: string; task: T.ChatBackgroundTask; onClose: () => void }) {
  const t = useT();
  const running = listed.status === 'running';
  // Read again while it runs, and once more as it ends, for its last lines.
  const detail = useQuery({
    queryKey: ['chat-background', chatRef, listed.id, listed.status],
    queryFn: () => api.chatBackgroundTask(chatRef, listed.id),
    refetchInterval: running ? 1500 : false,
    retry: false,
  });
  const task = detail.data?.task ?? listed;
  const [buf, setBuf] = useState<OutputBuffer>();
  const [loadingEarlier, setLoadingEarlier] = useState(false);
  useEffect(() => {
    if (detail.data && !detail.data.outputError) setBuf((held) => withTail(held, detail.data));
  }, [detail.data]);

  // The output follows its end until you scroll up, and stays where you
  // were reading when an earlier piece goes in above.
  const box = useRef<HTMLPreElement>(null);
  const following = useRef(true);
  const fromEnd = useRef(0);
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    el.scrollTop = following.current ? el.scrollHeight : el.scrollHeight - fromEnd.current;
  }, [buf]);

  const earlier = async () => {
    if (!buf || buf.start === 0) return;
    setLoadingEarlier(true);
    try {
      const d = await api.chatBackgroundTask(chatRef, listed.id, buf.start);
      if (!d.outputError) setBuf((held) => (held ? withEarlier(held, d) : held));
    } finally {
      setLoadingEarlier(false);
    }
  };

  const Icon = kindIcon[task.kind as keyof typeof kindIcon] ?? Workflow;
  const took = task.endedAt ? formatDuration(Math.max(0, (Date.parse(task.endedAt) - Date.parse(task.startedAt)) / 1000)) : undefined;
  const outputError = detail.data?.outputError || (detail.error ? t('chat.background.gone') : undefined);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-3xl gap-4" data-background-task={task.id}>
        <DialogHeader>
          <DialogTitle className="flex min-w-0 items-center gap-2 pr-8">
            <Icon className="size-4 shrink-0 text-subtle" strokeWidth={1.8} />
            <span className="min-w-0 break-words">{task.title}</span>
          </DialogTitle>
        </DialogHeader>
        <dl className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-ui">
          <dt className="text-subtle">{t('chat.background.field.status')}</dt>
          <dd className={cn('flex min-w-0 flex-wrap items-center gap-1.5', statusTone(task.status))} data-background-status={task.status}>
            <StatusIcon status={task.status} />
            <span>{t(statusKey(task.status))}</span>
            {task.exitCode != null && <span className="text-subtle">· {t('chat.background.exitCode', { code: task.exitCode })}</span>}
          </dd>
          <dt className="text-subtle">{t('chat.background.field.kind')}</dt>
          <dd className="text-secondary">{t(kindKey(task.kind))}</dd>
          {task.command && (
            <>
              <dt className="text-subtle">{t(task.kind === 'subagent' ? 'chat.background.field.task' : 'chat.background.field.command')}</dt>
              <dd className="min-w-0 whitespace-pre-wrap break-words font-mono text-caption text-secondary" data-background-command>
                {task.command}
              </dd>
            </>
          )}
          <dt className="text-subtle">{t('chat.background.field.started')}</dt>
          <dd className="tabular-nums text-secondary">
            {formatDateTime(task.startedAt)}
            {running && (
              <span className="text-subtle">
                {' · '}
                <Elapsed since={task.startedAt} />
              </span>
            )}
          </dd>
          {task.endedAt && (
            <>
              <dt className="text-subtle">{t('chat.background.field.ended')}</dt>
              <dd className="tabular-nums text-secondary">
                {formatDateTime(task.endedAt)}
                <span className="text-subtle"> · {t('chat.background.took', { time: took! })}</span>
              </dd>
            </>
          )}
          {task.summary && (
            <>
              <dt className="text-subtle">{t('chat.background.field.summary')}</dt>
              <dd className="min-w-0 break-words text-secondary">{task.summary}</dd>
            </>
          )}
        </dl>
        <div className="min-w-0">
          <div className="mb-1.5 flex items-center gap-2 text-ui text-subtle">
            <span>{t('chat.background.output')}</span>
            {running && buf && <span className="chat-shine text-caption">{t('chat.background.live')}</span>}
            {buf && buf.start > 0 && (
              <Button variant="ghost" size="sm" className="ml-auto h-6 px-2 text-caption" disabled={loadingEarlier} onClick={earlier} data-background-earlier>
                {t('chat.background.earlier')}
              </Button>
            )}
          </div>
          {buf && buf.text ? (
            <pre
              ref={box}
              className="max-h-[45vh] min-h-24 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-line-faint bg-surface-faint p-3 font-mono text-caption leading-relaxed text-secondary"
              data-background-output
              onScroll={(e) => {
                const el = e.currentTarget;
                fromEnd.current = el.scrollHeight - el.scrollTop;
                following.current = fromEnd.current - el.clientHeight < 24;
              }}
            >
              {buf.text}
            </pre>
          ) : (
            <p className="rounded-lg border border-line-faint bg-surface-faint px-3 py-2.5 text-ui text-subtle" data-background-no-output>
              {outputError
                ? t('chat.background.outputError', { error: outputError })
                : detail.isPending
                  ? t('chat.background.reading')
                  : task.hasOutput
                    ? t('chat.background.empty')
                    : t('chat.background.noOutput')}
            </p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
