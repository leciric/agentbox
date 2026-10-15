import { useMutation } from '@tanstack/react-query';
import { ExternalLink, ShieldAlert, ShieldCheck, ShieldX, ShieldOff } from 'lucide-react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { agentPermissionState } from '../../lib/chat';
import { useT } from '../../lib/i18n';
import { cn, errorMessage } from '../../lib/utils';
import { DiffView } from './ChangedFiles';
import { PermissionActions } from './PermissionActions';

// A link from a chat to one of its project's agents: App (and the phone)
// open that agent's view.
export const openAgentEvent = 'agentbox:open-agent';

export function openAgent(ref: string) {
  window.dispatchEvent(new CustomEvent(openAgentEvent, { detail: ref }));
}

// AgentPermissionCard is one of the project's agents asking permission in its
// own chat, shown in the lead's (T.ChatAgentPermission): the daemon keeps it in
// step with the agent's request, so it settles wherever that is answered. Its
// buttons answer the agent's request on the agent's own route, the one its
// chat's banner uses; the lead never answers it itself.
const outcomeKey = {
  waiting: 'chat.agentPermission.waiting',
  approved: 'chat.agentPermission.approved',
  approvedAlways: 'chat.agentPermission.approvedAlways',
  denied: 'chat.agentPermission.denied',
  gone: 'chat.agentPermission.gone',
} as const;

export function AgentPermissionCard({ item }: { item: T.ChatItem }) {
  const t = useT();
  const ap = item.agentPermission!;
  const { permission, tool } = ap;
  const answer = useMutation({ mutationFn: (option: string) => api.answerChat(ap.ref, ap.item, option), onError: (err) => toast.error(errorMessage(err)) });
  const state = agentPermissionState(permission);
  const waiting = state === 'waiting';
  const detail = tool?.command || tool?.paths?.[0];
  const Icon = state === 'approved' || state === 'approvedAlways' ? ShieldCheck : state === 'denied' ? ShieldX : state === 'gone' ? ShieldOff : ShieldAlert;
  const who = ap.title || ap.agent;
  return (
    <div
      className={cn('mb-4 min-w-0 rounded-xl border px-3.5 py-2.5 text-[13px]', waiting ? 'border-amber-300/30 bg-amber-400/[0.07]' : 'border-line bg-surface-faint')}
      data-chat-item="agent-permission"
      data-chat-id={item.id}
      data-agent-permission={state}
    >
      <div className="flex min-w-0 items-center gap-2">
        <Icon className={cn('size-4 shrink-0', waiting ? 'text-amber-300' : state === 'denied' ? 'text-rose-300' : state === 'gone' ? 'text-subtle' : 'text-emerald-300')} />
        <span className={cn('min-w-0 truncate font-medium', waiting ? 'text-amber-100' : 'text-secondary')} title={ap.agent}>
          {t('chat.agentPermission.asks', { agent: who, action: tool?.kind ?? '' })}
        </span>
        <button
          type="button"
          className="ml-auto flex shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-[12px] text-subtle transition hover:bg-surface-strong hover:text-primary"
          onClick={() => openAgent(ap.ref)}
          data-open-agent={ap.ref}
        >
          {t('chat.agentPermission.open')}
          <ExternalLink className="size-3.5" />
        </button>
      </div>
      <p className={cn('mt-1 break-words', waiting ? 'text-primary' : 'text-muted')} data-chat-text>
        {permission.title}
      </p>
      {detail && !permission.title.includes(detail) && (
        <code className="mt-1 block max-h-20 overflow-auto whitespace-pre-wrap break-all font-mono text-[11.5px] text-muted" data-agent-permission-detail>
          {detail}
        </code>
      )}
      {waiting && tool?.diffs?.length ? (
        <div className="mt-2 overflow-hidden rounded-lg border border-line bg-well">
          {tool.diffs.map((diff, i) => (
            <DiffView key={i} diff={diff} className="max-h-40" />
          ))}
        </div>
      ) : null}
      {waiting ? (
        <PermissionActions
          options={permission.options}
          agent={who}
          action={tool?.kind}
          pending={answer.isPending}
          sending={answer.variables}
          onAnswer={(id) => answer.mutate(id)}
          approveLabel={t('chat.agentPermission.approve')}
          denyLabel={t('chat.agentPermission.deny')}
        />
      ) : (
        <p className={cn('mt-1 text-[12px]', state === 'denied' ? 'text-rose-200/80' : state === 'gone' ? 'text-subtle' : 'text-emerald-200/80')} data-agent-permission-outcome>
          {t(outcomeKey[state])}
        </p>
      )}
    </div>
  );
}
