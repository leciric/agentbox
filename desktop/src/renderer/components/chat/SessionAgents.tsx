import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronDown, ChevronUp } from 'lucide-react';
import { useState } from 'react';
import type * as T from '../../../shared/api';
import { liveTarget, targetsOf } from '../../../shared/messageTarget';
import { api } from '../../lib/api';
import { chatKey, fetchThread, isMessage, peekThread } from '../../lib/chat';
import { agentChatStatus } from '../../lib/chatActivity';
import { useT } from '../../lib/i18n';
import { chooseTarget, useChosenTarget } from '../../lib/messageTarget';
import { requestComposerFocus } from '../../lib/newTask';
import { cn } from '../../lib/utils';
import { Tip } from '../ui/tooltip';
import { TargetDot } from './TargetChip';

// How many of an agent's latest messages its card shows, opened.
const recent = 6;

// SessionAgents is a card for each agent a session of the project's chat
// made, above its composer: its state, what it is doing, and what it said
// last, kept current by its chat's events as the agent list's rows are
// (peekThread). Opened, a card shows the agent's latest messages; clicked, it
// points the composer at that agent (lib/messageTarget.ts).
export function SessionAgents({ project, session, leadRef }: { project: string; session: string; leadRef: string }) {
  const t = useT();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const targets = targetsOf(agents.data ?? [], project, session);
  const target = liveTarget(useChosenTarget(leadRef), targets);
  const [open, setOpen] = useState<string>();
  const [hidden, setHidden] = useState(false);
  if (targets.own.length === 0) return null;
  const opened = targets.own.find((a) => a.name === open);
  return (
    <div className="mx-auto mb-2 w-full" data-session-agents>
      <div className="mb-1 flex items-center gap-1 px-1 text-[11px] font-medium uppercase tracking-wider text-subtle">
        <span>{t('chat.agents.title', { count: targets.own.length })}</span>
        <button className="ml-auto flex items-center gap-0.5 rounded px-1 normal-case tracking-normal text-faint transition hover:text-tertiary" onClick={() => setHidden((h) => !h)} aria-expanded={!hidden} data-session-agents-toggle>
          {hidden ? t('chat.agents.show') : t('chat.agents.hide')}
          {hidden ? <ChevronDown className="size-3" /> : <ChevronUp className="size-3" />}
        </button>
      </div>
      {!hidden && (
        <>
          <div className="flex gap-2 overflow-x-auto pb-1">
            {targets.own.map((a) => (
              <AgentCard key={a.ref} agent={a} targeted={a.name === target} open={a.name === open} onTarget={() => {
                  chooseTarget(leadRef, a.name);
                  requestComposerFocus(leadRef);
                }} onToggle={() => setOpen((o) => (o === a.name ? undefined : a.name))} />
            ))}
          </div>
          {opened && <Recent agent={opened} />}
        </>
      )}
    </div>
  );
}

// lastSaid is what an agent said last, or failing that the last message in
// its chat.
const lastSaid = (thread: T.ChatThread | undefined) => {
  const messages = (thread?.items ?? []).filter(isMessage);
  return (messages.findLast((it) => it.kind === 'assistant') ?? messages.at(-1))?.text?.trim();
};

function AgentCard({ agent, targeted, open, onTarget, onToggle }: { agent: T.Agent; targeted: boolean; open: boolean; onTarget: () => void; onToggle: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const thread = useQuery({ queryKey: chatKey(agent.ref), queryFn: () => peekThread(queryClient, agent.ref), staleTime: 30_000 });
  const status = agentChatStatus(agent, thread.data);
  const said = lastSaid(thread.data);
  return (
    <div
      className={cn(
        'group relative flex w-64 shrink-0 flex-col gap-0.5 rounded-xl border bg-surface-faint px-3 py-1.5 text-left transition',
        targeted ? 'border-brand-400/60 bg-brand-500/10' : 'border-line hover:border-line-strong hover:bg-surface-raised',
      )}
      data-session-agent={agent.name}
      data-targeted={targeted || undefined}
    >
      <button className="absolute inset-0 rounded-xl" aria-label={t('chat.agents.target', { name: agent.name })} onClick={onTarget} />
      <div className="flex min-w-0 items-center gap-1.5 text-[12.5px]">
        <TargetDot agent={agent} className="pointer-events-none" />
        <span className="pointer-events-none shrink-0 font-medium text-primary">{agent.name}</span>
        <span className="pointer-events-none min-w-0 flex-1 truncate text-subtle">{agent.title}</span>
        <Tip label={open ? t('chat.agents.less') : t('chat.agents.more')}>
          <button
            className="relative -mr-1 flex size-5 shrink-0 items-center justify-center rounded text-faint transition hover:bg-surface-raised hover:text-tertiary"
            onClick={onToggle}
            aria-expanded={open}
            aria-label={open ? t('chat.agents.less') : t('chat.agents.more')}
            data-session-agent-open
          >
            {open ? <ChevronUp className="size-3.5" /> : <ChevronDown className="size-3.5" />}
          </button>
        </Tip>
      </div>
      <p className={cn('pointer-events-none truncate text-[11.5px]', status.tone === 'urgent' ? 'text-amber-300' : status.tone === 'error' ? 'text-rose-300' : 'text-secondary')} data-session-agent-status>
        {status.command ? (
          <>
            {status.text} <code className="font-mono text-[11px] text-tertiary">{status.command}</code>
          </>
        ) : (
          status.text
        )}
      </p>
      <p className="pointer-events-none truncate text-[11.5px] text-muted" data-session-agent-said>
        {said || t('chat.agents.nothingYet')}
      </p>
    </div>
  );
}

// Recent is an opened card's agent's latest messages, read a page at a time
// as its chat is (fetchThread), into the same cache its card reads.
function Recent({ agent }: { agent: T.Agent }) {
  const t = useT();
  const queryClient = useQueryClient();
  const thread = useQuery({ queryKey: chatKey(agent.ref), queryFn: () => fetchThread(queryClient, agent.ref) });
  const messages = (thread.data?.items ?? []).filter(isMessage).slice(-recent);
  return (
    <div className="mt-1 max-h-56 overflow-y-auto rounded-xl border border-line bg-surface-faint px-3 py-2" data-session-agent-recent={agent.name}>
      {messages.length === 0 ? (
        <p className="text-[12px] text-subtle">{thread.isLoading ? t('common.loading') : t('chat.agents.nothingYet')}</p>
      ) : (
        messages.map((it) => (
          <div key={it.id} className={cn('py-1 text-[12px] leading-relaxed', it.kind === 'assistant' ? 'text-tertiary' : 'text-primary')}>
            <span className="mr-1.5 text-[11px] font-medium text-subtle">{it.kind === 'assistant' ? agent.name : t('chat.agents.you')}</span>
            <span className="whitespace-pre-wrap break-words">{(it.text ?? '').trim()}</span>
          </div>
        ))
      )}
    </div>
  );
}
