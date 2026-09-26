import { useQuery } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { choiceName } from '../lib/modelChoices';
import { humanTokens, tps, usd } from '../lib/tokens';
import { timeAgo } from '../lib/utils';
import { aiLabel, StateBadge } from './state';

// AgentInfoCard is what hovering an agent row shows, and what its context
// menu's "Info" item opens: everything about it that isn't already on the
// row itself. Its own chat session (model, effort, context) and its line of
// the token ledger (average TPS, tokens, cost) are fetched here, not passed
// in, so every place an agent is listed can show the same card without
// carrying that data around itself.
export function AgentInfoCard({ agent, pr }: { agent: T.Agent; pr?: T.PullRequest }) {
  const [project, name] = agent.ref.split('/');
  const chat = useQuery({ queryKey: ['chat', agent.ref], queryFn: () => api.chat(agent.ref), staleTime: 10_000 });
  const spend = useQuery({ queryKey: ['tokens', project, name, 'all'], queryFn: () => api.tokens({ project, agent: name }), staleTime: 10_000 });

  const options = chat.data?.session.options ?? [];
  const find = (category: string) => options.find((o) => o.category === category && o.type === 'select');
  const label = (option: T.ChatOption | undefined) => (option ? choiceName(option.choices.find((c) => c.value === option.value) ?? { value: option.value, name: option.value }) : undefined);
  const model = label(find('model'));
  const effort = label(find('thought_level'));
  const session = chat.data?.session;
  const mine = spend.data?.agents?.[0];

  return (
    <div className="grid w-64 gap-2" data-agent-info={agent.ref}>
      <div className="flex items-center justify-between gap-2">
        <span className="min-w-0 truncate text-[13px] font-medium text-primary">{agent.title || agent.name}</span>
        <StateBadge state={agent.state} />
      </div>
      <div className="grid min-w-0 gap-1">
        <Row label="AI tool" value={aiLabel(agent.ai)} />
        {model && <Row label="Model" value={model} />}
        {effort && <Row label="Effort" value={effort} />}
        {session?.contextSize ? <Row label="Context" value={`${humanTokens(session.contextUsed ?? 0)} of ${humanTokens(session.contextSize)}`} /> : null}
        <Row label="Avg TPS" value={mine ? tps(mine.avgTPS ?? 0) : '—'} />
        <Row label="Claude account" value={agent.claudeAccount || '—'} />
        <Row label="GitHub account" value={agent.githubAccount || '—'} />
        <Row label="Branch" value={agent.branch} mono />
        <Row label="Uptime" value={timeAgo(agent.createdAt)} />
        <Row label="CPU" value={agent.limits.configuredCPU || agent.limits.cpu || '—'} />
        <Row label="Memory" value={agent.limits.memory || '—'} />
        <Row label="Tokens" value={mine ? `${humanTokens(mine.total)} · ${usd(mine.costUSD)}` : '0'} />
        {pr && <Row label="Pull request" value={`#${pr.number} ${pr.state}`} />}
      </div>
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: ReactNode; mono?: boolean }) {
  return (
    <div className="flex min-w-0 items-baseline justify-between gap-3 text-[11.5px]">
      <span className="shrink-0 text-faint">{label}</span>
      <span className={mono ? 'min-w-0 truncate font-mono text-[11px] text-secondary' : 'min-w-0 truncate text-secondary'}>{value}</span>
    </div>
  );
}
