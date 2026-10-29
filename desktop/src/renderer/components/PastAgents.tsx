import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, CalendarDays, GitBranch, LoaderCircle } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api, ApiError } from '../lib/api';
import { useT } from '../lib/i18n';
import { asAgent, pastAgentsKey } from '../lib/pastAgents';
import { workKey } from '../lib/work';
import { projectLabel } from '../lib/projectName';
import { useChatOpenAt } from '../lib/reveal';
import { humanTokens } from '../lib/tokens';
import { errorMessage, timeAgo } from '../lib/utils';
import { ChatTab } from './chat/ChatTab';
import { ConfirmDialog } from './ConfirmDialog';
import { JobProgress } from './JobProgress';
import { PrChip } from './PrChip';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Textarea } from './ui/input';
import { Skeleton } from './ui/skeleton';

// PastAgentView is one past agent, an agent whose machine was removed, read
// in its chat: what it was, its pull request, and the chat itself, read-only,
// with Continue and Delete under it. A search that found a message here opens
// it at that message. Past agents are listed with the live ones' work on the
// Work page (WorkView), which opens this.
export function PastAgentView({ agentRef, onBack, onSelect }: { agentRef: string; onBack: () => void; onSelect: (view: View) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [project, name] = agentRef.split('/');
  const past = useQuery({ queryKey: [...pastAgentsKey, 'one', agentRef], queryFn: () => api.archivedAgent(agentRef), retry: false });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const openAt = useChatOpenAt(project, name);
  const [continuing, setContinuing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const a = past.data;

  return (
    <div className="flex h-full min-h-0 flex-col" data-past-chat={agentRef}>
      <div className="w-full shrink-0 px-4 pb-3 pt-4 md:px-8">
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
          <Button size="sm" variant="ghost" onClick={onBack} data-past-back>
            <ArrowLeft />
            {t('pastAgents.back')}
          </Button>
          {a && (
            <div className="grid min-w-0 flex-1 gap-0.5">
              <div className="flex min-w-0 items-baseline gap-2">
                <h1 className="truncate text-[15px] font-semibold tracking-tight text-title">{a.title || a.name}</h1>
                {a.title && <span className="shrink-0 font-mono text-[11.5px] text-subtle">{a.name}</span>}
              </div>
              <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[11.5px] text-subtle">
                <span className="max-w-48 truncate">{projectLabel(a.project, projects.data)}</span>
                {a.branch && (
                  <span className="flex min-w-0 items-center gap-1 font-mono">
                    <GitBranch className="size-3 shrink-0" />
                    <span className="truncate">{a.branch}</span>
                  </span>
                )}
                <span className="flex items-center gap-1" title={new Date(a.archivedAt).toLocaleString()}>
                  <CalendarDays className="size-3 shrink-0" />
                  {t('pastAgents.archived', { when: timeAgo(a.archivedAt) })}
                </span>
                {a.tokens > 0 && <span className="font-mono tabular-nums">{t('pastAgents.tokens', { tokens: humanTokens(a.tokens) })}</span>}
                {a.pr && a.pr.number > 0 && (
                  <button
                    className="flex min-w-0 max-w-full items-center gap-1.5 hover:text-title"
                    title={t('pastAgents.openPr', { number: a.pr.number })}
                    onClick={() => void window.agentbox.openExternal(a.pr!.url)}
                    data-past-pr-link
                  >
                    <PrChip pr={a.pr} dataAttr="data-past-pr-chip" />
                    <span className="truncate">{a.pr.title}</span>
                  </button>
                )}
              </div>
            </div>
          )}
        </div>
      </div>
      <div className="mx-2 mb-2 flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-line md:mx-6 md:mb-6">
        {past.isPending ? (
          <div className="grid gap-3 p-6">
            <Skeleton className="h-4 w-2/3" />
            <Skeleton className="h-4 w-1/2" />
          </div>
        ) : !a ? (
          <div className="p-6">
            <Notice>{past.error instanceof ApiError && past.error.status === 404 ? t('pastAgents.gone') : errorMessage(past.error)}</Notice>
          </div>
        ) : (
          <ChatTab
            agent={asAgent(a)}
            starting={false}
            onStart={() => {}}
            autoStart={false}
            openAt={openAt}
            onOpenAgent={(ref) => onSelect({ kind: 'agent', ref })}
            readOnly={{ onContinue: () => setContinuing(true), onDelete: () => setDeleting(true) }}
          />
        )}
      </div>
      <ContinueDialog agent={continuing ? (a ?? null) : null} onClose={() => setContinuing(false)} onContinued={(ref) => onSelect({ kind: 'agent', ref })} />
      <DeletePastDialog
        agents={deleting && a ? [a] : null}
        onClose={() => setDeleting(false)}
        onDeleted={() => {
          onBack();
          void queryClient.invalidateQueries({ queryKey: pastAgentsKey });
          void queryClient.invalidateQueries({ queryKey: workKey });
        }}
      />
    </div>
  );
}

// DeletePastDialog asks before deleting past agents for good, one or many:
// the record and the chat go, the branch and the media stay.
function DeletePastDialog({ agents, onClose, onDeleted }: { agents: T.ArchivedAgent[] | null; onClose: () => void; onDeleted: (refs: string[]) => void }) {
  const t = useT();
  const one = agents?.length === 1 ? agents[0] : undefined;
  return (
    <ConfirmDialog
      open={agents !== null}
      onOpenChange={(open) => !open && onClose()}
      title={one ? t('pastAgents.deleteTitle', { name: one.title || one.name }) : t('pastAgents.deleteManyTitle', { count: agents?.length ?? 0 })}
      description={t('pastAgents.deleteDescription')}
      confirmLabel={t('common.delete')}
      destructive
      onConfirm={async () => {
        if (one) {
          await api.deleteArchived(one.ref);
          toast.success(t('pastAgents.deleted', { count: 1 }), { description: one.title || one.name });
          return onDeleted([one.ref]);
        }
        const result = await api.deleteArchivedMany(agents!.map((a) => a.ref));
        onDeleted(result.deleted);
        if (result.deleted.length > 0) toast.success(t('pastAgents.deleted', { count: result.deleted.length }));
        const failed = Object.entries(result.failed ?? {});
        if (failed.length > 0) toast.error(t('pastAgents.deleteFailed', { count: failed.length }), { description: failed.map(([ref, err]) => `${ref}: ${err}`).join('\n') });
      }}
    />
  );
}

// ContinueDialog starts a new agent where a past one left off, on its branch,
// with an optional message, and follows the job the way a new agent's is
// followed: the new agent opens when it's ready.
function ContinueDialog({ agent, onClose, onContinued }: { agent: T.ArchivedAgent | null; onClose: () => void; onContinued: (ref: string) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const open = agent !== null;
  const [message, setMessage] = useState('');
  const [job, setJob] = useState<T.Job | null>(null);
  const [finished, setFinished] = useState<T.Job | null>(null);
  const start = useMutation({
    mutationFn: () => api.continueArchived(agent!.ref, { message: message.trim() || undefined }),
    onSuccess: setJob,
  });
  useEffect(() => {
    if (!open) return;
    setMessage('');
    setJob(null);
    setFinished(null);
    start.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, agent?.ref]);

  const onDone = async (done: T.Job) => {
    setFinished(done);
    if (done.status !== 'succeeded') return;
    const created = done.result as T.Agent;
    await queryClient.invalidateQueries({ queryKey: ['agents'] });
    void queryClient.invalidateQueries({ queryKey: pastAgentsKey });
    void queryClient.invalidateQueries({ queryKey: workKey });
    toast(t('pastAgents.continued', { agent: created.title || created.name }));
    onClose();
    onContinued(created.ref);
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('pastAgents.continueTitle', { name: agent?.title || agent?.name || '' })}</DialogTitle>
          <DialogDescription>{t('pastAgents.continueDescription')}</DialogDescription>
        </DialogHeader>
        {job ? (
          <>
            <JobProgress jobId={job.id} onDone={(done) => void onDone(done)} />
            <DialogFooter>
              <Button onClick={onClose}>{finished ? t('common.close') : t('project.newAgent.keepInBackground')}</Button>
            </DialogFooter>
          </>
        ) : (
          <form
            className="grid gap-4"
            onSubmit={(event) => {
              event.preventDefault();
              start.mutate();
            }}
          >
            <Field label={t('pastAgents.continueMessage')} htmlFor="past-continue-message">
              <Textarea
                id="past-continue-message"
                autoFocus
                value={message}
                placeholder={t('pastAgents.continuePlaceholder')}
                onChange={(event) => setMessage(event.target.value)}
                data-past-continue-message
              />
            </Field>
            {start.error && <Notice>{errorMessage(start.error)}</Notice>}
            <DialogFooter>
              <Button variant="ghost" onClick={onClose}>
                {t('common.cancel')}
              </Button>
              <Button type="submit" variant="primary" disabled={start.isPending}>
                {start.isPending && <LoaderCircle className="animate-spin" />}
                {t('pastAgents.continue')}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
