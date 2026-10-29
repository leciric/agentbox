import { ChevronDown, Crown } from 'lucide-react';
import type * as T from '../../../shared/api';
import type { Targets } from '../../../shared/messageTarget';
import { chatLabel, type StatusTone } from '../../lib/agentStatus';
import { useT } from '../../lib/i18n';
import { keysHint, targetKeys } from '../../lib/shortcuts';
import { cn } from '../../lib/utils';
import { Menu, MenuContent, MenuLabel, MenuSeparator, MenuTrigger } from '../ui/menu';
import { Tip } from '../ui/tooltip';
import * as MenuPrimitive from '@radix-ui/react-dropdown-menu';

// The dot beside each agent: how the rail sums it up, in one colour.
export const targetDot: Record<StatusTone, string> = {
  urgent: 'bg-amber-400 animate-pulse',
  error: 'bg-rose-400',
  warning: 'bg-amber-400/70',
  live: 'bg-sky-400 animate-pulse',
  muted: 'bg-faint',
};

export function TargetDot({ agent, className }: { agent: T.Agent; className?: string }) {
  const label = chatLabel(agent);
  return <span title={label.text} className={cn('inline-block size-2 shrink-0 rounded-full', targetDot[label.tone], className)} />;
}

// TargetChip is the "To:" at the left of a project chat's composer: who the
// message goes to, the lead or one of the project's agents, the session's own
// listed first. once marks a target set by "@agent-31 " at the start of the
// message, which lasts for that message only.
export function TargetChip({ targets, target, once, onChoose }: { targets: Targets; target: string | undefined; once?: boolean; onChoose: (agent: string | undefined) => void }) {
  const t = useT();
  const hint = t('chat.target.tip', { next: keysHint(targetKeys.next), previous: keysHint(targetKeys.previous), lead: keysHint(targetKeys.lead) });
  const item = (agent: T.Agent | undefined) => (
    <MenuPrimitive.Item
      key={agent?.name ?? ''}
      onSelect={() => onChoose(agent?.name)}
      className={cn(
        'flex cursor-default select-none items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-[13px] text-secondary outline-none data-[highlighted]:bg-surface-raised',
        (agent?.name ?? undefined) === target && 'text-primary',
      )}
      data-target-option={agent?.name ?? 'lead'}
    >
      {agent ? <TargetDot agent={agent} /> : <Crown className="size-3.5 shrink-0 text-subtle" />}
      <span className="shrink-0 font-medium">{agent ? agent.name : t('chat.target.lead')}</span>
      {agent?.title && <span className="min-w-0 flex-1 truncate text-subtle">{agent.title}</span>}
    </MenuPrimitive.Item>
  );
  return (
    <Menu>
      <Tip label={hint} side="top">
        <MenuTrigger asChild>
          <button
            type="button"
            className={cn(
              'flex h-8 min-w-0 max-w-44 shrink items-center gap-1 rounded-full px-2.5 text-[12px] transition',
              target ? 'bg-brand-500/15 text-brand-200 ring-1 ring-brand-400/40 hover:bg-brand-500/25' : 'text-subtle hover:bg-surface-raised hover:text-primary',
              once && 'italic',
            )}
            aria-label={t('chat.target.label', { target: target ?? t('chat.target.lead') })}
            data-target-chip={target ?? 'lead'}
            data-target-once={once || undefined}
          >
            <span className="shrink-0 text-faint">{t('chat.target.to')}</span>
            <span className="min-w-0 truncate font-medium">{target ?? t('chat.target.lead')}</span>
            <ChevronDown className="size-3 shrink-0 opacity-70" />
          </button>
        </MenuTrigger>
      </Tip>
      <MenuContent align="start" side="top" className="max-h-96 w-72 overflow-y-auto">
        {item(undefined)}
        {targets.own.length > 0 && (
          <>
            <MenuSeparator />
            <MenuLabel>{t('chat.target.own')}</MenuLabel>
            {targets.own.map(item)}
          </>
        )}
        {targets.others.length > 0 && (
          <>
            <MenuSeparator />
            <MenuLabel>{t('chat.target.others')}</MenuLabel>
            {targets.others.map(item)}
          </>
        )}
        <div className="border-t border-line px-2.5 pb-1 pt-2 text-[11px] leading-relaxed text-faint">{hint}</div>
      </MenuContent>
    </Menu>
  );
}
