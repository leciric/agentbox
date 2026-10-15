import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Archive, ArrowLeft, ArrowRight, CalendarDays, GitBranch, GitPullRequest, LoaderCircle, Search, Trash2 } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api, ApiError } from '../lib/api';
import { useT, type MessageKey } from '../lib/i18n';
import { asAgent, filtered, pastAgentsKey, toggled, type PastPR, type PastSince } from '../lib/pastAgents';
import { projectLabel } from '../lib/projectName';
import { useChatOpenAt } from '../lib/reveal';
import { sinceTime } from '../lib/searchOpen';
import { humanTokens } from '../lib/tokens';
import { cn, errorMessage, timeAgo } from '../lib/utils';
import { ChatTab } from './chat/ChatTab';
import { ConfirmDialog } from './ConfirmDialog';
import { JobProgress } from './JobProgress';
import { Badge, type BadgeVariant } from './ui/badge';
import { Button } from './ui/button';
import { Notice, Panel } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input, Textarea } from './ui/input';
import { Select, SelectOption } from './ui/select';
import { Skeleton } from './ui/skeleton';

// The same colours and words as the Pull requests tab's state badges.
const prVariants: Record<string, BadgeVariant> = { open: 'info', merged: 'success', closed: 'default' };
const prLabels: Record<string, MessageKey> = { open: 'memory.pulls.state.open', merged: 'memory.pulls.state.merged', closed: 'memory.pulls.state.closed' };

// How long the search box waits for the typing to pause.
const debounceMs = 250;

// PastAgents is the agents whose machine was removed, kept with their chats
// until deleted: of one project (its Past agents tab), or of every project
// (project unset). open is the one being read, in its chat; onOpen opens one,
// or goes back to the list with null.
export function PastAgents({
  project,
  filter,
  open,
  onOpen,
  onSelect,
}: {
  project?: string;
  // filter is v2's project filter, beside the title of the page for every
  // project (a section of Settings there); project is what it picked.
  filter?: ReactNode;
  open?: string;
  onOpen: (ref: string | null) => void;
  onSelect: (view: View) => void;
}) {
  const t = useT();
  if (open) return <PastAgentChat agentRef={open} showProject={!project} onBack={() => onOpen(null)} onSelect={onSelect} />;
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-7">
        {(!project || filter) && (
          <div className="mb-5 flex flex-wrap items-end gap-3">
            <div>
              <h1 className="text-2xl font-semibold tracking-tight text-title">{t('pastAgents.title')}</h1>
              <p className="mt-1 text-sm text-muted">{t('pastAgents.description')}</p>
            </div>
            {filter && <div className="ml-auto">{filter}</div>}
          </div>
        )}
        <PastAgentsList key={project ?? ''} project={project} onOpen={onOpen} />
      </div>
    </div>
  );
}

// PastAgentsList is the list itself, with its search and filters, for a page
// or for Home (limit, with onShowAll to the whole list).
export function PastAgentsList({ project, limit, onOpen, onShowAll }: { project?: string; limit?: number; onOpen: (ref: string) => void; onShowAll?: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [query, setQuery] = useState('');
  const [q, setQ] = useState('');
  const [pr, setPR] = useState<PastPR>('');
  const [since, setSince] = useState<PastSince>('');
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [deleting, setDeleting] = useState<T.ArchivedAgent[] | null>(null);
  useEffect(() => {
    const timer = setTimeout(() => setQ(query.trim()), debounceMs);
    return () => clearTimeout(timer);
  }, [query]);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects, enabled: !project });
  const list = useQuery({
    queryKey: [...pastAgentsKey, project ?? '', q, pr, since, limit ?? 0],
    // since is worked out as it's read, so the key stays the same while it is.
    queryFn: () => api.archivedAgents({ project, q, pr, since: sinceTime(since), limit }),
    placeholderData: keepPreviousData,
  });
  const agents = list.data ?? [];
  const chosen = agents.filter((a) => selected.has(a.ref));
  const all = agents.length > 0 && chosen.length === agents.length;

  return (
    <div className="grid min-w-0 gap-3" data-past-agents={project ?? ''}>
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <div className="relative min-w-48 flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-subtle" />
          <Input className="pl-8" aria-label={t('pastAgents.search')} placeholder={t('pastAgents.search')} value={query} spellCheck={false} onChange={(e) => setQuery(e.target.value)} data-past-search />
        </div>
        <Select className="w-44" aria-label={t('pastAgents.pr.label')} value={pr} onChange={(v) => setPR(v as PastPR)} data-past-pr>
          <SelectOption value="">{t('pastAgents.pr.all')}</SelectOption>
          <SelectOption value="open">{t('pastAgents.pr.open')}</SelectOption>
          <SelectOption value="merged">{t('pastAgents.pr.merged')}</SelectOption>
          <SelectOption value="closed">{t('pastAgents.pr.closed')}</SelectOption>
          <SelectOption value="none">{t('pastAgents.pr.none')}</SelectOption>
        </Select>
        <Select className="w-40" aria-label={t('pastAgents.since.label')} value={since} onChange={(v) => setSince(v as PastSince)} data-past-since>
          <SelectOption value="">{t('pastAgents.since.any')}</SelectOption>
          <SelectOption value="day">{t('pastAgents.since.day')}</SelectOption>
          <SelectOption value="week">{t('pastAgents.since.week')}</SelectOption>
          <SelectOption value="month">{t('pastAgents.since.month')}</SelectOption>
        </Select>
      </div>

      <Panel className="overflow-hidden" aria-busy={list.isPending}>
        {agents.length > 0 && (
          <div className="flex min-h-10 items-center gap-3 border-b border-line-faint px-4 py-1.5 text-[12px] text-subtle">
            <input
              type="checkbox"
              aria-label={t('pastAgents.selectAll')}
              checked={all}
              onChange={() => setSelected(all ? new Set() : new Set(agents.map((a) => a.ref)))}
              data-past-select-all
            />
            <span className="min-w-0 flex-1 truncate">{chosen.length > 0 ? t('pastAgents.selected', { count: chosen.length }) : t('pastAgents.count', { count: agents.length })}</span>
            {list.isFetching && <LoaderCircle className="size-3.5 shrink-0 animate-spin" />}
            {chosen.length > 0 && (
              <Button size="sm" variant="danger" className="h-7" onClick={() => setDeleting(chosen)} data-past-delete-selected>
                <Trash2 />
                {t('common.delete')}
              </Button>
            )}
          </div>
        )}
        {list.isPending ? (
          <div className="divide-y divide-line-faint">
            {['w-48', 'w-36', 'w-56'].map((width) => (
              <div key={width} className="flex items-center gap-3 px-4 py-3">
                <Skeleton className={cn('h-3.5', width)} />
                <Skeleton className="ml-auto h-3 w-14" />
              </div>
            ))}
          </div>
        ) : list.error ? (
          <div className="p-4">
            <Notice>{errorMessage(list.error)}</Notice>
          </div>
        ) : agents.length === 0 ? (
          <div className="flex flex-col items-center gap-2 px-4 py-8 text-center text-[13px] text-subtle" data-past-empty>
            <Archive className="size-5 text-faint" />
            {filtered(q, pr, since) ? t('pastAgents.noMatch') : t('pastAgents.empty')}
          </div>
        ) : (
          <ul className="divide-y divide-line-faint">
            {agents.map((a) => (
              <PastAgentRow
                key={a.ref}
                agent={a}
                projectName={project ? undefined : projectLabel(a.project, projects.data)}
                selected={selected.has(a.ref)}
                onToggle={() => setSelected((s) => toggled(s, a.ref))}
                onOpen={() => onOpen(a.ref)}
                onDelete={() => setDeleting([a])}
              />
            ))}
          </ul>
        )}
      </Panel>
      {onShowAll && limit !== undefined && agents.length >= limit && (
        <button className="flex items-center gap-1 justify-self-start text-[13px] font-medium text-brand-300 hover:text-brand-200" onClick={onShowAll} data-past-show-all>
          {t('pastAgents.showAll')}
          <ArrowRight className="size-3.5" />
        </button>
      )}

      <DeletePastDialog
        agents={deleting}
        onClose={() => setDeleting(null)}
        onDeleted={(refs) => {
          setSelected((s) => new Set([...s].filter((ref) => !refs.includes(ref))));
          void queryClient.invalidateQueries({ queryKey: pastAgentsKey });
        }}
      />
    </div>
  );
}

function PastAgentRow({
  agent: a,
  projectName,
  selected,
  onToggle,
  onOpen,
  onDelete,
}: {
  agent: T.ArchivedAgent;
  projectName?: string;
  selected: boolean;
  onToggle: () => void;
  onOpen: () => void;
  onDelete: () => void;
}) {
  const t = useT();
  return (
    <li className={cn('flex min-w-0 items-center gap-3 px-4 py-2.5 transition-colors hover:bg-surface-faint', selected && 'bg-surface-faint')} data-past-agent={a.ref}>
      <input type="checkbox" aria-label={t('pastAgents.select', { name: a.title || a.name })} checked={selected} onChange={onToggle} />
      <button className="grid min-w-0 flex-1 gap-0.5 text-left" onClick={onOpen}>
        <span className="flex min-w-0 items-baseline gap-2">
          <span className="truncate text-[13px] font-medium text-primary">{a.title || a.name}</span>
          {a.title && <span className="shrink-0 font-mono text-[11px] text-subtle">{a.name}</span>}
        </span>
        <span className="flex min-w-0 items-center gap-2 text-[11.5px] text-subtle">
          {projectName && <span className="max-w-[40%] shrink-0 truncate">{projectName}</span>}
          {a.branch && (
            <span className="flex min-w-0 items-center gap-1 font-mono">
              <GitBranch className="size-3 shrink-0" />
              <span className="truncate">{a.branch}</span>
            </span>
          )}
        </span>
      </button>
      {a.pr && a.pr.number > 0 && <PRBadge pr={a.pr} />}
      {a.tokens > 0 && <span className="hidden shrink-0 font-mono text-[11px] tabular-nums text-subtle sm:inline">{t('pastAgents.tokens', { tokens: humanTokens(a.tokens) })}</span>}
      <span className="shrink-0 text-[11.5px] text-subtle" title={new Date(a.archivedAt).toLocaleString()}>
        {timeAgo(a.archivedAt)}
      </span>
      <Button size="icon" variant="ghost" className="size-7 shrink-0" aria-label={t('pastAgents.deleteOne', { name: a.title || a.name })} onClick={onDelete}>
        <Trash2 className="size-3.5" />
      </Button>
    </li>
  );
}

function PRBadge({ pr }: { pr: T.PullRequest }) {
  const t = useT();
  return (
    <Badge variant={prVariants[pr.state] ?? 'default'} className="shrink-0" title={pr.title}>
      <GitPullRequest />#{pr.number}
      {prLabels[pr.state] && <span className="hidden sm:inline">{t(prLabels[pr.state])}</span>}
    </Badge>
  );
}

// PastAgentChat is one past agent read in its chat: what it was, its pull
// request, and the chat itself, read-only, with Continue and Delete under it.
// A search that found a message here opens it at that message.
function PastAgentChat({ agentRef, showProject, onBack, onSelect }: { agentRef: string; showProject: boolean; onBack: () => void; onSelect: (view: View) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [project, name] = agentRef.split('/');
  const past = useQuery({ queryKey: [...pastAgentsKey, 'one', agentRef], queryFn: () => api.archivedAgent(agentRef), retry: false });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects, enabled: showProject });
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
                {showProject && <span className="max-w-48 truncate">{projectLabel(a.project, projects.data)}</span>}
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
                    <PRBadge pr={a.pr} />
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
          return onDeleted([one.ref]);
        }
        const result = await api.deleteArchivedMany(agents!.map((a) => a.ref));
        onDeleted(result.deleted);
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
