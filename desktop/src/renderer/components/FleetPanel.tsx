import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CircleDot, GitBranch, GitPullRequest, Image, LoaderCircle, Moon } from 'lucide-react';
import { toast } from 'sonner';
import { useState } from 'react';
import type { View } from '../App';
import * as T from '../../shared/api';
import { api } from '../lib/api';
import { githubErrorSentence, timeAgo } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { AgentAvatar, StateBadge } from './state';
import { Button } from './ui/button';
import { Badge } from './ui/badge';
import { EmptyState } from './ui/card';

// FleetPanel follows a project's agents: what each one is for, what it is doing
// now, how much it has changed, what it has shown, and where its branch stands
// on GitHub. Agents still being created are shown too, so you watch them appear.
export function FleetPanel({ project, onSelect }: { project: string; onSelect: (view: View) => void }) {
  const queryClient = useQueryClient();
  // Pull requests come from the daemon's cache and announce themselves on the
  // event stream when they move (D54), so this poll is only here for what
  // isn't announced: an agent's diff growing as it works.
  const fleet = useQuery({
    queryKey: ['fleet', project],
    queryFn: () => api.fleet(project),
    refetchInterval: 10_000,
  });
  const data = fleet.data;
  const [freeing, setFreeing] = useState(false);
  // Stopping frees the memory an idle agent holds and keeps its disk, so it
  // comes back in seconds. Nothing is lost: the work is on its branch.
  const retire = useMutation({
    mutationFn: () => api.retire(project, { how: 'stop' }),
    onSuccess: async (result) => {
      const freed = result.retired.length;
      toast(freed === 0 ? 'Nothing to free' : `Stopped ${freed} finished agent${freed === 1 ? '' : 's'}`, {
        description: freed === 0 ? undefined : 'Their work stays on their branches. Start them again any time.',
      });
      await queryClient.invalidateQueries({ queryKey: ['fleet', project] });
      await queryClient.invalidateQueries({ queryKey: ['agents'] });
    },
    onError: (err) => toast.error(String(err)),
  });

  if (!data) {
    return <div className="p-6 text-sm text-subtle">{fleet.error ? 'The fleet is unavailable.' : 'Loading…'}</div>;
  }
  if (data.agents.length === 0 && data.creating.length === 0) {
    return (
      <div className="panel rounded-2xl">
        <EmptyState icon={GitBranch} title="No agents yet">Ask {project}&apos;s chat for what you want built, or create an agent yourself.</EmptyState>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3">
      {/* Not having a GitHub account is nothing to warn about here: the Pull
          requests tab is one click away and says it properly. Reading them
          and failing is worth a line, because the tab looks empty otherwise. */}
      {data.githubError && data.githubError.kind !== T.GitHubNoAccount && (
        <p className="text-xs text-amber-300/90">{githubErrorSentence(data.githubError)} Pull requests say more.</p>
      )}
      {data.idle > 0 && (
        <div className="panel flex flex-wrap items-center gap-3 rounded-2xl px-4 py-3" data-fleet-idle={data.idle}>
          <Moon className="size-4 shrink-0 text-muted" />
          <p className="min-w-0 flex-1 text-[13px] text-tertiary">
            {data.idle} agent{data.idle === 1 ? ' has' : 's have'} finished and {data.idle === 1 ? 'is' : 'are'} still holding a machine.
          </p>
          <Button size="sm" variant="ghost" disabled={retire.isPending} onClick={() => setFreeing(true)}>
            Free their machines
          </Button>
        </div>
      )}
      {data.creating.map((job) => (
        <div key={job.id} className="panel flex items-center gap-3 rounded-2xl px-4 py-3.5" data-fleet-creating>
          <LoaderCircle className="size-5 shrink-0 animate-spin text-brand-300" />
          <div className="min-w-0">
            <p className="text-[13.5px] text-secondary">A new agent is being created</p>
            <p className="truncate text-xs text-subtle">{job.target}</p>
          </div>
        </div>
      ))}
      {data.agents.map((agent) => (
        <button
          key={agent.ref}
          type="button"
          onClick={() => onSelect({ kind: 'agent', ref: agent.ref })}
          className="panel w-full rounded-2xl px-4 py-3.5 text-left transition-colors hover:bg-surface"
          data-fleet-agent={agent.name}
        >
          <div className="flex flex-wrap items-center gap-3">
            <AgentAvatar ai={agent.ai} state={agent.state} />
            <div className="min-w-0 flex-1">
              <p className="flex min-w-0 items-center gap-2">
                <span className="truncate text-[14px] font-medium text-primary">{agent.title || agent.name}</span>
                {agent.title && <span className="shrink-0 font-mono text-[11.5px] text-subtle">{agent.name}</span>}
              </p>
              <p className="truncate font-mono text-[11.5px] text-subtle">{agent.branch}</p>
            </div>
            <StateBadge state={agent.state} />
          </div>
          <div className="mt-2.5 flex flex-wrap items-center gap-x-4 gap-y-1.5 text-[12px] text-muted">
            <Doing chat={agent.chat} />
            <Changes changes={agent.changes} />
            {agent.media > 0 && (
              <span className="flex items-center gap-1.5">
                <Image className="size-3.5" />
                {agent.media} item{agent.media === 1 ? '' : 's'}
              </span>
            )}
            <PR pr={agent.pr} />
            {agent.idle && (
              <Badge className="gap-1" title={agent.retire.safe ? `Its work is on ${agent.retire.branch}` : agent.retire.reason}>
                <Moon className="size-3" />
                idle
              </Badge>
            )}
            <span className="ml-auto text-faint">{timeAgo(agent.createdAt)}</span>
          </div>
        </button>
      ))}
      <ConfirmDialog
        open={freeing}
        onOpenChange={setFreeing}
        title="Free the finished agents' machines?"
        description="Their machines are shut down, which frees the memory they hold. Nothing is lost: each agent's work stays on its branch, its worktree stays on disk, and starting it again takes seconds. Agents that are still working, or have uncommitted changes, are left alone."
        confirmLabel="Free them"
        onConfirm={async () => {
          await retire.mutateAsync();
        }}
      />
    </div>
  );
}

// Doing is what the agent's chat is up to right now.
const doing: Record<string, { label: string; className: string }> = {
  running: { label: 'Working', className: 'text-sky-300' },
  waiting: { label: 'Waiting for you', className: 'text-amber-300' },
  starting: { label: 'Starting', className: 'text-muted' },
  ready: { label: 'Idle', className: 'text-subtle' },
  error: { label: 'Its AI tool stopped', className: 'text-rose-300' },
};

function Doing({ chat }: { chat?: string }) {
  const state = doing[chat ?? ''];
  if (!state) return null;
  return (
    <span className={`flex items-center gap-1.5 ${state.className}`} data-fleet-doing={chat}>
      <CircleDot className="size-3.5" />
      {state.label}
    </span>
  );
}

function Changes({ changes }: { changes: T.AgentChanges }) {
  if (changes.files === 0) return <span className="text-faint">No changes yet</span>;
  return (
    <span className="flex items-center gap-1.5" data-fleet-changes>
      <GitBranch className="size-3.5" />
      {changes.files} file{changes.files === 1 ? '' : 's'}
      <span className="text-emerald-400">+{changes.insertions}</span>
      <span className="text-rose-400">−{changes.deletions}</span>
      {changes.dirty && <span className="text-subtle">uncommitted</span>}
    </span>
  );
}

const checks: Record<string, string> = { passing: 'text-emerald-400', failing: 'text-rose-400', pending: 'text-amber-300' };

function PR({ pr }: { pr?: T.PullRequest }) {
  if (!pr) return null;
  return (
    <span className="flex items-center gap-1.5" data-fleet-pr={pr.number} title={pr.title}>
      <GitPullRequest className="size-3.5" />
      #{pr.number} {pr.draft ? 'draft' : pr.state}
      {pr.checks && <span className={checks[pr.checks] ?? 'text-muted'}>checks {pr.checks}</span>}
      {(pr.comments ?? 0) > 0 && <span className="text-subtle">{pr.comments} comments</span>}
    </span>
  );
}
