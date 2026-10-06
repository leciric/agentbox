import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { chatKey, fetchThread } from '../lib/chat';
import { prChecksText, prState } from '../lib/agentStatus';
import { t as tt, useT } from '../lib/i18n';
import { choiceName } from '../lib/modelChoices';
import { humanTokens, tps, usd } from '../lib/tokens';
import { humanBytes, timeAgo } from '../lib/utils';
import { aiLabel, StateBadge } from './state';

// AgentInfoCard is what hovering an agent row shows, and what its context
// menu's "Info" item opens: everything about it that isn't already on the
// row itself. Its own chat session (model, effort, context) and its line of
// the token ledger (average TPS, tokens, cost) are fetched here, not passed
// in, so every place an agent is listed can show the same card without
// carrying that data around itself.
export function AgentInfoCard({ agent, pr }: { agent: T.Agent; pr?: T.PullRequest }) {
  const t = useT();
  const [project, name] = agent.ref.split('/');
  // The chat's own query, read the way the chat reads it: its latest page.
  const queryClient = useQueryClient();
  const chat = useQuery({ queryKey: chatKey(agent.ref), queryFn: () => fetchThread(queryClient, agent.ref), staleTime: 10_000 });
  const spend = useQuery({ queryKey: ['tokens', project, name, 'all'], queryFn: () => api.tokens({ project, agent: name }), staleTime: 10_000 });
  // Disk is measured by the daemon, which walks the whole worktree and asks
  // Incus for the machine's volume, and caches both for a minute: the card
  // asks no more often than that however often it's hovered.
  const disk = useQuery({ queryKey: ['agentDisk', agent.ref], queryFn: () => api.agentDisk(agent.ref), staleTime: 60_000 });

  const options = chat.data?.session?.options ?? [];
  const find = (category: string) => options.find((o) => o.category === category && o.type === 'select');
  const label = (option: T.ChatOption | undefined) => (option ? choiceName(option.choices.find((c) => c.value === option.value) ?? { value: option.value, name: option.value }) : undefined);
  const model = label(find('model'));
  const effort = label(find('thought_level'));
  const session = chat.data?.session;
  const mine = spend.data?.agents?.[0];

  return (
    // grid-cols-1 is minmax(0, 1fr): an auto column would grow to the title's
    // full width, pushing every value past the card's right edge.
    <div className="grid w-64 grid-cols-1 gap-2" data-agent-info={agent.ref}>
      <div className="flex items-center justify-between gap-2">
        <span className="min-w-0 truncate text-[13px] font-medium text-primary">{agent.title || agent.name}</span>
        <span className="shrink-0">
          <StateBadge state={agent.state} />
        </span>
      </div>
      {agent.title && <span className="-mt-1.5 min-w-0 truncate font-mono text-[11px] text-faint">{agent.name}</span>}
      <div className="grid min-w-0 gap-1">
        <Row label={t('agent.info.aiTool')} value={aiLabel(agent.ai)} />
        {model && <Row label={t('agent.info.model')} value={model} />}
        {effort && <Row label={t('agent.info.effort')} value={effort} />}
        {session?.contextSize ? <Row label={t('agent.info.context')} value={t('agent.info.contextOf', { used: humanTokens(session.contextUsed ?? 0), size: humanTokens(session.contextSize) })} /> : null}
        <Row label={t('agent.info.avgTps')} value={mine ? tps(mine.avgTPS ?? 0) : '—'} />
        <Row label={t('agent.info.claudeAccount')} value={agent.claudeAccount || '—'} />
        <Row label={t('agent.info.githubAccount')} value={agent.githubAccount || '—'} />
        <Row label={t('agent.info.branch')} value={agent.branch} mono />
        <Row label={t('agent.info.uptime')} value={timeAgo(agent.createdAt)} />
        <Row label={t('agent.info.machineDisk')} value={diskSize(disk.data?.machine, disk.isPending)} />
        <Row label={t('agent.info.worktreeDisk')} value={diskSize(disk.data?.worktree, disk.isPending)} />
        <Row label={t('agent.info.tokens')} value={mine ? `${humanTokens(mine.total)} · ${usd(mine.costUSD)}` : '0'} />
        {pr && <Row label={t('agent.info.pullRequest')} value={prSummary(pr)} />}
      </div>
    </div>
  );
}

// diskSize is one of the agent's disk sizes, or why there isn't one: still
// being measured, or not measurable (a machine Incus can't read).
function diskSize(bytes: number | undefined, pending: boolean): string {
  if (bytes !== undefined) return humanBytes(bytes);
  return pending ? '…' : '—';
}

function Row({ label, value, mono }: { label: string; value: ReactNode; mono?: boolean }) {
  return (
    <div className="flex min-w-0 items-baseline justify-between gap-3 text-[11.5px]">
      <span className="shrink-0 text-faint">{label}</span>
      <span className={mono ? 'min-w-0 truncate font-mono text-[11px] text-secondary' : 'min-w-0 truncate text-secondary'}>{value}</span>
    </div>
  );
}

// prSummary is the pull request in a line: its state, and what the watch found
// wrong with it when it found something.
function prSummary(pr: T.PullRequest): string {
  const parts = [`#${pr.number} ${prState(pr)}`];
  if (pr.checks) parts.push(tt('agent.pr.checks', { checks: prChecksText(pr.checks) }));
  if (pr.conflict) parts.push(tt('agent.pr.conflicts'));
  if (pr.review === 'changes_requested') parts.push(tt('agent.pr.changesRequested'));
  return parts.join(', ');
}
