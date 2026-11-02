import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, Bot, CalendarDays, History } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { pastState } from '../../lib/pastAgents';
import { errorMessage, timeAgo } from '../../lib/utils';
import { ConfirmDialog } from '../ConfirmDialog';
import { Button } from '../ui/button';
import { EmptyState, Notice } from '../ui/card';
import { Skeleton } from '../ui/skeleton';
import { ChatTab } from './ChatTab';

// closedSessionsKey is the chat's history, under the open sessions' key so
// that what changes them (lib/events.ts) reads it again too.
export const closedSessionsKey = (project: string) => ['leadSessions', project, 'closed'] as const;

// useClosedSessions is the sessions in a chat's history, the most recently
// closed first.
export function useClosedSessions(project: string) {
  return useQuery({ queryKey: closedSessionsKey(project), queryFn: () => api.closedLeadSessions(project) });
}

// closedSessionAgent is a closed session's lead as ChatTab takes an agent: in
// the state a past agent wears, which nothing starts or wakes.
function closedSessionAgent(s: T.LeadSession): T.Agent {
  return {
    ref: s.ref,
    project: s.project,
    name: s.ref.split('/')[1] ?? '',
    title: s.topic,
    instance: '',
    ai: 'claude',
    autonomous: false,
    branch: '',
    baseRef: '',
    baseCommit: '',
    worktree: '',
    source: '',
    claudeAccount: '',
    githubAccount: '',
    interface: 'chat',
    state: pastState,
    ip: '',
    createdAt: s.createdAt,
  };
}

// SessionHistory is a chat's closed sessions, where its tabs' History button
// leads, as past agents are to the live ones (PastAgents.tsx): a list, the
// most recently closed first, and one read in its chat, read-only, with
// Delete, Reopen and New session from it under it. Reopening puts it back
// among the tabs as it was; a new session from it is a session of its own,
// started on where it left off (daemon seedSession).
export function SessionHistory({
  project,
  onBack,
  onReopened,
  onContinued,
}: {
  project: string;
  onBack: () => void;
  onReopened: (s: T.LeadSession) => void;
  onContinued: (s: T.LeadSession) => void;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const closed = useClosedSessions(project);
  const [reading, setReading] = useState<string>();
  const [deleting, setDeleting] = useState<T.LeadSession>();
  const shown = closed.data?.find((s) => s.id === reading);
  const refresh = () => void queryClient.invalidateQueries({ queryKey: ['leadSessions', project] });

  const reopen = useMutation({
    mutationFn: (id: string) => api.reopenLeadSession(project, id),
    onSuccess: (s) => {
      refresh();
      onReopened(s);
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const continueFrom = useMutation({
    mutationFn: (id: string) => api.newLeadSession(project, { continueFrom: id }),
    onSuccess: onContinued,
    onError: (err) => toast.error(errorMessage(err)),
  });

  const header = shown ? (
    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
      <Button size="sm" variant="ghost" onClick={() => setReading(undefined)} data-history-back>
        <ArrowLeft />
        {t('sessions.backToHistory')}
      </Button>
      <div className="grid min-w-0 flex-1 gap-0.5">
        <h1 className="truncate text-heading font-semibold tracking-tight text-title">{shown.topic}</h1>
        <SessionMeta s={shown} />
      </div>
    </div>
  ) : (
    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
      <Button size="sm" variant="ghost" onClick={onBack} data-history-back>
        <ArrowLeft />
        {t('sessions.backToChat')}
      </Button>
      <h1 className="min-w-0 flex-1 truncate text-heading font-semibold tracking-tight text-title">{t('sessions.historyTitle')}</h1>
    </div>
  );

  return (
    <div className="flex h-full min-h-0 flex-col" data-session-history>
      <div className="w-full shrink-0 px-4 pb-3 pt-4 md:px-8">{header}</div>
      <div className="mx-2 mb-2 flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-line md:mx-6 md:mb-6">
        {closed.isPending ? (
          <div className="grid gap-3 p-6">
            <Skeleton className="h-4 w-2/3" />
            <Skeleton className="h-4 w-1/2" />
          </div>
        ) : closed.error ? (
          <div className="p-6">
            <Notice>{errorMessage(closed.error)}</Notice>
          </div>
        ) : shown ? (
          <ChatTab
            key={shown.ref}
            agent={closedSessionAgent(shown)}
            starting={false}
            onStart={() => {}}
            autoStart={false}
            readOnly={{
              note: t('sessions.readOnly'),
              continueLabel: t('sessions.continue'),
              onContinue: () => continueFrom.mutate(shown.id),
              onReopen: () => reopen.mutate(shown.id),
              onDelete: () => setDeleting(shown),
            }}
          />
        ) : closed.data.length === 0 ? (
          <EmptyState icon={History} title={t('sessions.historyEmpty')} />
        ) : (
          <div className="min-h-0 flex-1 overflow-y-auto">
            <p className="px-4 pb-1 pt-3 text-caption text-subtle md:px-5">{t('sessions.historyHint')}</p>
            <ul className="grid gap-px p-2">
              {closed.data.map((s) => (
                <li key={s.id}>
                  <button
                    type="button"
                    onClick={() => setReading(s.id)}
                    data-history-session={s.id}
                    className="grid w-full min-w-0 gap-1 rounded-xl px-3 py-2.5 text-left transition-colors hover:bg-surface-raised"
                  >
                    <span className="truncate text-ui font-medium text-primary">{s.topic}</span>
                    <SessionMeta s={s} />
                    {s.last?.text && <span className="line-clamp-2 min-w-0 break-words text-caption text-muted">{s.last.text}</span>}
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
      <ConfirmDialog
        open={deleting !== undefined}
        onOpenChange={(open) => !open && setDeleting(undefined)}
        title={t('sessions.deleteTitle', { topic: deleting?.topic ?? '' })}
        description={t('sessions.deleteDescription')}
        confirmLabel={t('common.delete')}
        destructive
        onConfirm={async () => {
          const s = deleting!;
          await api.deleteLeadSession(project, s.id);
          toast.success(t('sessions.deleted', { topic: s.topic }));
          setReading(undefined);
          refresh();
          void queryClient.invalidateQueries({ queryKey: ['agents'] });
        }}
      />
    </div>
  );
}

// SessionMeta is a closed session's dates and agents, on one line.
function SessionMeta({ s }: { s: T.LeadSession }) {
  const t = useT();
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-caption text-subtle">
      <span className="flex items-center gap-1" title={new Date(s.activeAt).toLocaleString()}>
        <CalendarDays className="size-3 shrink-0" />
        {t('sessions.activeAgo', { when: timeAgo(s.activeAt) })}
      </span>
      {s.closedAt && <span title={new Date(s.closedAt).toLocaleString()}>{t('sessions.closedAgo', { when: timeAgo(s.closedAt) })}</span>}
      {s.agents > 0 && (
        <span className="flex items-center gap-1">
          <Bot className="size-3 shrink-0" />
          {t('sessions.agentCount', { count: s.agents })}
        </span>
      )}
    </span>
  );
}
