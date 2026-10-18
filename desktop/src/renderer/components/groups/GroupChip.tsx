import { useQuery } from '@tanstack/react-query';
import { ArrowRight, Sparkles, Split } from 'lucide-react';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { memberSt, openCompare, projectGroups, ready, toolName, type MemberSt } from '../../lib/groups';
import { useT } from '../../lib/i18n';
import { cn } from '../../lib/utils';
import { AIIcon } from '../state';
import { Button } from '../ui/button';

// useGroups is every project's open and recently decided groups, read once
// for the sidebar and the lead chats; EventGroup refreshes it.
export function useGroups() {
  return useQuery({ queryKey: ['groups'], queryFn: api.groups, refetchInterval: 60_000 });
}

// GroupChips are a project's open groups, one chip each above its agents in
// the sidebar: what was run, how many have finished, and a way into the
// Compare view.
export function GroupChips({ project, activeId }: { project: string; activeId?: string }) {
  const groups = projectGroups(useGroups().data, project);
  if (groups.length === 0) return null;
  return (
    <>
      {groups.map((g) => (
        <GroupChip key={g.id} group={g} active={g.id === activeId} />
      ))}
    </>
  );
}

function GroupChip({ group: g, active }: { group: T.Group; active: boolean }) {
  const t = useT();
  const done = ready(g);
  return (
    <button
      type="button"
      onClick={() => openCompare(g.id)}
      aria-current={active ? 'page' : undefined}
      className={cn(
        'group flex w-full min-w-0 items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[12px] outline-none transition-colors focus-visible:ring-2 focus-visible:ring-brand-400',
        active ? 'bg-surface-raised' : 'hover:bg-surface',
      )}
      title={t('group.chip.tip', { title: g.title })}
      data-group-chip={g.id}
      data-group-ready={done || undefined}
    >
      <span className={cn('flex size-7 shrink-0 items-center justify-center rounded-lg', done ? 'bg-brand-500/20 text-brand-200' : 'bg-surface-strong text-subtle')}>
        <Split className="size-3.5" />
      </span>
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate font-medium leading-[18px] text-primary">{g.title}</span>
        <span className={cn('truncate text-[11px] leading-4', done ? 'font-medium text-brand-200' : 'text-subtle')}>
          {done ? t('group.chip.ready', { count: g.members.length }) : t('group.chip.working', { finished: g.finished, count: g.members.length })}
        </span>
      </span>
      <span className="flex shrink-0 -space-x-1">
        {g.members.map((m) => (
          <span key={m.label} className={cn('flex size-4 items-center justify-center rounded bg-surface-strong font-mono text-[9px] font-semibold ring-1 ring-sidebar', dotTone[memberSt(g, m)])}>
            {m.label}
          </span>
        ))}
      </span>
    </button>
  );
}

const dotTone: Record<MemberSt, string> = {
  creating: 'text-faint',
  failed: 'text-rose-300',
  working: 'text-sky-200',
  finished: 'text-emerald-200',
  kept: 'text-brand-100',
  retired: 'text-faint',
};

// GroupLetter is a group member's letter on its row in the sidebar, so the
// agents being compared read as one group.
export function GroupLetter({ agentRef }: { agentRef: string }) {
  const t = useT();
  const groups = useGroups().data;
  for (const g of groups ?? []) {
    if (g.settledAt) continue;
    const m = g.members.find((x) => x.ref === agentRef);
    if (m)
      return (
        <span className="shrink-0 rounded bg-brand-500/15 px-1 font-mono text-[10px] font-semibold text-brand-200" title={t('group.letterTip', { title: g.title, label: m.label })} data-group-letter={m.label}>
          {m.label}
        </span>
      );
  }
  return null;
}

// GroupCard is a group in its lead's chat, where the daemon's notice tells of
// it (api.ChatItem.Group): made, all finished, or decided. It reads the group
// as it is now, not as the notice found it.
export function GroupCard({ item }: { item: T.ChatItem }) {
  const t = useT();
  const g = useGroups().data?.find((x) => x.id === item.group);
  if (!g) return null;
  const done = ready(g);
  const status = g.settledAt
    ? g.kept
      ? t('group.status.kept', { agent: g.kept })
      : t('group.status.closed')
    : done
      ? t('group.status.ready')
      : t('group.status.working', { finished: g.finished, count: g.members.length });
  return (
    <div className="mb-4 overflow-hidden rounded-xl border border-line-strong bg-surface" data-chat-item="group" data-chat-id={item.id} data-group-card={g.id}>
      <div className="flex min-w-0 items-center gap-2.5 px-3.5 py-3">
        <span className={cn('flex size-8 shrink-0 items-center justify-center rounded-lg', done ? 'bg-brand-500/20 text-brand-200' : 'bg-surface-strong text-subtle')}>
          <Split className="size-4" />
        </span>
        <span className="grid min-w-0 flex-1">
          <span className="truncate font-medium text-title">{g.title}</span>
          <span className="truncate text-[12px] text-subtle">
            {t('group.card.line', { count: g.members.length })} · {status}
          </span>
        </span>
        <Button size="sm" variant={done && !g.settledAt ? 'primary' : 'secondary'} onClick={() => openCompare(g.id)} data-group-compare>
          {t('group.card.compare')}
          <ArrowRight />
        </Button>
      </div>
      <div className="flex flex-wrap gap-1.5 border-t border-line px-3.5 py-2.5">
        {g.members.map((m) => (
          <span key={m.label} className="inline-flex min-w-0 items-center gap-1.5 rounded-md bg-surface-strong px-2 py-1 text-[11.5px] text-secondary">
            <span className="font-mono font-semibold text-title">{m.label}</span>
            <AIIcon ai={m.ai} className="size-3.5 shrink-0" />
            <span className="truncate">{m.model || toolName(m.ai)}</span>
            <span className={cn('text-[10.5px]', dotTone[memberSt(g, m)])}>{t(`group.st.${memberSt(g, m)}`)}</span>
            {g.recommended && g.recommended === m.agent && <Sparkles className="size-3 text-brand-300" />}
          </span>
        ))}
      </div>
    </div>
  );
}
