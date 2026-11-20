// The Running view: what each agent's machine still has running, as the
// daemon groups it (internal/agent/processes.go): a process the agent or its
// terminal started is the root of a tree with everything it started, so a dev
// server is one row, whatever it forked. Each tree the daemon doesn't protect
// has a Kill, confirmed in place; the AI tool's own and the machine's are
// shown, locked, so an agent's figures add up. The dialog opens from the top
// bar's machine popovers (lib/running.ts) on the open project, and each
// agent's Resources panel shows its own list.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronRight, LoaderCircle, Lock, Skull } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type { View } from '../App';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatNumber, useT } from '../lib/i18n';
import { agentWithoutTree, closeRunning, span, splitTrees, upFor, useRunningRequest, withoutTree } from '../lib/running';
import { cn, errorMessage, humanBytes } from '../lib/utils';
import { Button } from './ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from './ui/dialog';
import { Tip } from './ui/tooltip';

// refreshEvery is how often an open list is read again.
const refreshEvery = 4000;

// RunningDialogHost is the Running dialog wherever the top bar is: it opens
// when lib/running.ts asks it to.
export function RunningDialogHost({ onSelect }: { onSelect: (view: View) => void }) {
  const request = useRunningRequest();
  return <RunningDialog open={request.open} project={request.project} onOpenChange={(open) => !open && closeRunning()} onSelect={onSelect} />;
}

export function RunningDialog({
  open,
  project,
  onOpenChange,
  onSelect,
}: {
  open: boolean;
  project?: string;
  onOpenChange: (open: boolean) => void;
  onSelect: (view: View) => void;
}) {
  const t = useT();
  // On the project it opened on, unless the user widens it.
  const [all, setAll] = useState(false);
  const scope = project && !all ? project : undefined;
  const key = ['processes', scope ?? ''];
  const query = useQuery({ queryKey: key, queryFn: () => api.processes(scope), enabled: open, refetchInterval: open ? refreshEvery : false });
  const agents = query.data?.agents ?? [];
  const biggest = Math.max(1, ...agents.flatMap((a) => a.trees.map((tr) => tr.memory)));
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) setAll(false);
        onOpenChange(next);
      }}
    >
      {/* Nothing focused as it opens: its first control is the scope's
          tab, and a ring on it says nothing. Tab still walks it. */}
      <DialogContent className="max-w-2xl gap-4" data-running-dialog={scope ?? 'all'} onOpenAutoFocus={(event) => event.preventDefault()}>
        <DialogHeader>
          <DialogTitle>{t('running.title')}</DialogTitle>
          <DialogDescription>{t('running.description')}</DialogDescription>
        </DialogHeader>
        {project && (
          <div className="flex w-fit items-center gap-0.5 rounded-lg bg-surface-raised p-0.5 text-caption" role="tablist">
            {[
              { on: !all, label: t('running.scopeProject'), set: false },
              { on: all, label: t('running.scopeAll'), set: true },
            ].map((s) => (
              <button
                key={s.label}
                type="button"
                role="tab"
                aria-selected={s.on}
                className={cn('rounded-md px-2.5 py-1 transition', s.on ? 'bg-surface-strong font-medium text-primary' : 'text-muted hover:text-primary')}
                onClick={() => setAll(s.set)}
              >
                {s.label}
              </button>
            ))}
          </div>
        )}
        {query.isPending ? (
          <span className="flex items-center gap-2 py-6 text-ui text-muted">
            <LoaderCircle className="size-4 animate-spin" />
            {t('running.loading')}
          </span>
        ) : query.isError ? (
          <span className="py-2 text-ui text-rose-300">{errorMessage(query.error)}</span>
        ) : agents.length === 0 ? (
          <span className="py-6 text-ui text-muted">{scope ? t('running.noneProject') : t('running.none')}</span>
        ) : (
          <div className="grid min-w-0 gap-4">
            {agents.map((a) => (
              <section key={a.ref} className="grid min-w-0 gap-1.5" data-running-agent={a.ref}>
                <AgentHeading agent={a} showProject={!scope} onOpen={() => {
                    onOpenChange(false);
                    onSelect({ kind: 'agent', ref: a.ref });
                  }} />
                <TreeList agent={a} biggest={biggest} queryKey={key} />
              </section>
            ))}
          </div>
        )}
        <p className="text-caption text-subtle">{t('running.note')}</p>
      </DialogContent>
    </Dialog>
  );
}

function AgentHeading({ agent, showProject, onOpen }: { agent: T.AgentProcesses; showProject: boolean; onOpen: () => void }) {
  const t = useT();
  return (
    <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5 border-b border-line-faint pb-1.5">
      <button type="button" className="min-w-0 truncate text-left text-ui font-medium text-primary transition hover:underline" onClick={onOpen}>
        {agent.title || agent.name}
      </button>
      {(showProject || agent.title) && <span className="truncate font-mono text-caption text-faint">{showProject ? agent.ref : agent.name}</span>}
      {agent.running && (
        <span className="ml-auto flex shrink-0 items-baseline gap-2 text-caption text-muted">
          <span className="font-mono tabular-nums">{t('running.agentTotals', { memory: humanBytes(agent.memory), count: agent.processes })}</span>
          {agent.killable > 0 && <span className="text-faint">{t('running.killable', { memory: humanBytes(agent.killable) })}</span>}
        </span>
      )}
    </div>
  );
}

// AgentProcessList is one agent's trees, for its Resources panel.
export function AgentProcessList({ agentRef }: { agentRef: string }) {
  const t = useT();
  const key = ['agentProcesses', agentRef];
  const query = useQuery({ queryKey: key, queryFn: () => api.agentProcesses(agentRef), refetchInterval: refreshEvery });
  const a = query.data;
  const biggest = Math.max(1, ...(a?.trees ?? []).map((tr) => tr.memory));
  return (
    <section className="grid min-w-0 gap-2 rounded-xl border border-line bg-surface-faint px-3 py-2.5" data-resource="running" data-agent-processes={agentRef}>
      <div className="flex min-w-0 items-baseline gap-2">
        <span className="text-ui font-medium text-title">{t('running.title')}</span>
        {a?.running && (
          <span className="ml-auto truncate font-mono text-caption tabular-nums text-muted">{t('running.agentTotals', { memory: humanBytes(a.memory), count: a.processes })}</span>
        )}
      </div>
      {query.isPending ? (
        <span className="flex items-center gap-2 text-caption text-muted">
          <LoaderCircle className="size-3.5 animate-spin" />
          {t('running.loading')}
        </span>
      ) : query.isError ? (
        <span className="text-caption text-rose-300">{errorMessage(query.error)}</span>
      ) : (
        <TreeList agent={a!} biggest={biggest} queryKey={key} single />
      )}
    </section>
  );
}

// TreeList is an agent's trees, the small ones folded away until asked for.
function TreeList({ agent, biggest, queryKey, single }: { agent: T.AgentProcesses; biggest: number; queryKey: unknown[]; single?: boolean }) {
  const t = useT();
  const [showSmall, setShowSmall] = useState(false);
  if (agent.error) return <span className="text-caption text-rose-300">{t('running.couldntRead', { error: agent.error })}</span>;
  if (!agent.running) return <span className="text-caption text-muted">{t('running.notRunning')}</span>;
  const { shown, small } = splitTrees(agent.trees);
  const rows = showSmall ? [...shown.slice(0, -1), ...small, ...shown.slice(-1)] : shown;
  return (
    <div className="grid min-w-0 gap-1">
      {rows.map((tree) => (
        <TreeRow key={`${tree.kind}-${tree.pid}`} agent={agent} tree={tree} biggest={biggest} queryKey={queryKey} single={single} />
      ))}
      {small.length > 0 && (
        <button type="button" className="justify-self-start px-1 text-caption text-brand-400 transition hover:underline" onClick={() => setShowSmall(!showSmall)}>
          {showSmall ? t('running.hideSmall') : t('running.small', { count: small.length, memory: humanBytes(small.reduce((n, tr) => n + tr.memory, 0)) })}
        </button>
      )}
    </div>
  );
}

function TreeRow({ agent, tree, biggest, queryKey, single }: { agent: T.AgentProcesses; tree: T.ProcessTree; biggest: number; queryKey: unknown[]; single?: boolean }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [asking, setAsking] = useState(false);
  const [members, setMembers] = useState(false);
  const label = tree.kind === 'system' ? t('running.system') : tree.label;
  const kill = useMutation({
    mutationFn: () => api.killProcess(agent.ref, tree.pid),
    onSuccess: (res) => {
      setAsking(false);
      toast.success(t('running.killed', { label: res.label, memory: humanBytes(res.memory), agent: agent.title || agent.name }));
      // Gone from every list showing it at once, before the next reading.
      queryClient.setQueriesData<T.ProcessList>({ queryKey: ['processes'] }, (list) => list && withoutTree(list, agent.ref, tree.pid));
      queryClient.setQueryData<T.AgentProcesses>(['agentProcesses', agent.ref], (a) => a && agentWithoutTree(a, tree.pid));
      void queryClient.invalidateQueries({ queryKey });
      void queryClient.invalidateQueries({ queryKey: ['memoryUsage'] });
    },
    onError: (err) => {
      setAsking(false);
      toast.error(t('running.killFailed', { label, error: errorMessage(err) }));
    },
  });
  const up = upFor(tree.started);
  const facts = [
    t('running.processes', { count: tree.processes }),
    t('running.cpu', { percent: formatNumber(tree.cpu, { maximumFractionDigits: tree.cpu < 10 ? 1 : 0 }) }),
    up && t('running.up', { span: up }),
  ].filter(Boolean);
  return (
    <div
      className={cn('group grid min-w-0 gap-1 rounded-lg px-2 py-1.5 transition', asking ? 'bg-rose-500/10 ring-1 ring-inset ring-rose-400/30' : 'hover:bg-surface-raised')}
      data-process-tree={tree.pid}
      data-kind={tree.kind}
      data-heavy={tree.heavy || undefined}
      data-idle={tree.idle || undefined}
    >
      <div className="flex min-w-0 items-center gap-2">
        {tree.members.length > 1 ? (
          <button
            type="button"
            className="-ml-1 shrink-0 rounded p-0.5 text-faint transition hover:bg-surface-strong hover:text-primary"
            aria-label={members ? t('running.hideMembers') : t('running.showMembers')}
            aria-expanded={members}
            onClick={() => setMembers(!members)}
          >
            <ChevronRight className={cn('size-3.5 transition-transform', members && 'rotate-90')} />
          </button>
        ) : (
          <span className="w-4 shrink-0" />
        )}
        <Tip label={<span className="break-all font-mono">{tree.command}</span>} side="top">
          <span className={cn('min-w-0 flex-1 truncate text-ui', tree.protected ? 'text-muted' : 'font-mono text-primary')}>{label}</span>
        </Tip>
        <span className={cn('shrink-0 font-mono text-ui tabular-nums', tree.heavy ? 'text-amber-200' : 'text-secondary')}>{humanBytes(tree.memory)}</span>
        <div className="flex shrink-0 items-center justify-end gap-1.5" style={{ minWidth: single ? undefined : '5.5rem' }}>
          {tree.protected ? (
            <Tip label={tree.kind === 'tool' ? t('running.protectedTool') : t('running.protectedSystem')} side="left">
              <span className="flex items-center gap-1 text-caption text-faint" data-protected>
                <Lock className="size-3" />
                {t('running.protected')}
              </span>
            </Tip>
          ) : kill.isPending ? (
            <span className="flex items-center gap-1.5 text-caption text-rose-200">
              <LoaderCircle className="size-3.5 animate-spin" />
              {t('running.killing')}
            </span>
          ) : asking ? null : (
            <Button variant="danger" size="sm" className="h-7 px-2 text-caption" onClick={() => setAsking(true)} data-kill={tree.pid}>
              <Skull className="!size-3.5" />
              {t('running.kill')}
            </Button>
          )}
        </div>
      </div>
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 pl-6 text-caption text-muted">
        <span className="h-1 w-16 shrink-0 overflow-hidden rounded-full bg-surface-strong">
          <span className={cn('block h-full rounded-full', tree.heavy ? 'bg-amber-400' : tree.protected ? 'bg-slate-500' : 'bg-brand-400')} style={{ width: `${Math.max(2, (tree.memory / biggest) * 100)}%` }} />
        </span>
        <span className="truncate">{facts.join(' · ')}</span>
        {tree.heavy && (
          <Tip label={t('running.heavyTip', { memory: humanBytes(tree.memory) })}>
            <span className="rounded-full bg-amber-400/10 px-1.5 py-px text-amber-200">{t('running.heavy')}</span>
          </Tip>
        )}
        {tree.idle && (
          <Tip label={t('running.idleTip', { span: span(tree.idleSeconds) })}>
            <span className="rounded-full bg-sky-400/10 px-1.5 py-px text-sky-300">{t('running.idle', { span: span(tree.idleSeconds) })}</span>
          </Tip>
        )}
      </div>
      {asking && !kill.isPending && (
        <div className="flex flex-wrap items-center gap-2 pl-6 pt-0.5" data-kill-confirm={tree.pid}>
          <span className="text-caption text-rose-200">{t('running.killAsk', { count: tree.processes })}</span>
          <Button variant="destructive" size="sm" className="h-7 px-2.5 text-caption" onClick={() => kill.mutate()} autoFocus>
            {t('running.kill')}
          </Button>
          <Button variant="ghost" size="sm" className="h-7 px-2.5 text-caption" onClick={() => setAsking(false)}>
            {t('common.cancel')}
          </Button>
        </div>
      )}
      {members && (
        <div className="grid min-w-0 gap-0.5 pl-6" data-members={tree.pid}>
          {tree.members.map((m) => (
            <div key={m.pid} className="flex min-w-0 items-baseline gap-2 font-mono text-caption text-faint">
              <span className="w-12 shrink-0 tabular-nums">{m.pid}</span>
              <span className="min-w-0 flex-1 truncate" title={m.command}>
                {m.command}
              </span>
              <span className="shrink-0 tabular-nums">{humanBytes(m.memory)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
