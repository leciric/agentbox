// The verb bar, the prototype navigation's (lib/uiVerbs.ts) one column of
// places, always on screen at the window's left edge, narrow windows too:
// Ask (the leads' chats), Watch (the agents' machines), Review (their pull
// requests and proof), and at its foot Settings. Each verb carries the count
// that would make you go there: Ask what needs you, Watch what's working,
// Review what waits for a verdict, read where every other count is
// (useAgentCounts).
import { useQuery } from '@tanstack/react-query';
import { ClipboardCheck, MessagesSquare, MonitorPlay, Settings, type LucideIcon } from 'lucide-react';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT, type MessageKey } from '../../lib/i18n';
import { useAgentCounts } from '../../lib/useAgentCounts';
import { verbOf, type Verb } from '../../lib/uiVerbs';
import { cn } from '../../lib/utils';
import { Logo } from '../Logo';
import { Tip } from '../ui/tooltip';

const verbs: { verb: Verb; icon: LucideIcon; label: MessageKey; hint: MessageKey; view: View }[] = [
  { verb: 'ask', icon: MessagesSquare, label: 'verbs.ask', hint: 'verbs.ask.hint', view: { kind: 'homeChat' } },
  { verb: 'watch', icon: MonitorPlay, label: 'verbs.watch', hint: 'verbs.watch.hint', view: { kind: 'watch' } },
  { verb: 'review', icon: ClipboardCheck, label: 'verbs.review', hint: 'verbs.review.hint', view: { kind: 'review' } },
];

export function VerbBar({ view, onSelect }: { view: View; onSelect: (view: View) => void }) {
  const t = useT();
  const counts = useAgentCounts();
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup });
  const on = verbOf(view);
  const badge: Record<Verb, { count: number; tone: string } | undefined> = {
    ask: counts.all.needs > 0 ? { count: counts.all.needs, tone: 'bg-amber-400 text-black' } : undefined,
    watch: counts.all.working > 0 ? { count: counts.all.working, tone: 'bg-sky-400/90 text-black' } : undefined,
    review: counts.all.review > 0 ? { count: counts.all.review, tone: 'bg-emerald-400 text-black' } : undefined,
    settings: undefined,
  };
  // Settings, at the foot, is its icon alone: no name of it fits the column
  // in every language.
  const item = (verb: Verb, Icon: LucideIcon, label: string, hint: string, to: View, alert?: boolean, named = true) => {
    const active = on === verb;
    const b = badge[verb];
    return (
      <Tip key={verb} label={hint} side="right">
        <button
          type="button"
          aria-label={label}
          aria-current={active ? 'page' : undefined}
          // Pressed again where you are, a verb goes back to its start: Ask
          // to Home, Watch to every agent, Review to what waits.
          onClick={() => onSelect(to)}
          className={cn(
            'relative flex w-full flex-col items-center gap-1 rounded-xl px-1 py-2 text-[11px] font-medium leading-none transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50',
            active ? 'bg-surface-raised text-title' : 'text-muted hover:bg-surface hover:text-primary',
          )}
          data-verb={verb}
        >
          {active && <span className="absolute inset-y-2 left-0 w-0.5 rounded-full bg-brand-400" aria-hidden />}
          <Icon className={cn('size-5', active ? 'text-brand-300' : 'text-subtle')} />
          {named && <span className="max-w-full truncate">{label}</span>}
          {b && (
            <span className={cn('absolute right-1 top-1 min-w-4 rounded-full px-1 text-center text-[10px] font-semibold tabular-nums leading-4', b.tone)} data-verb-count={b.count}>
              {b.count > 99 ? '99+' : b.count}
            </span>
          )}
          {alert && <span className="absolute right-2 top-1.5 size-1.5 rounded-full bg-amber-400" title={t('shell.sidebar.needsAttention')} />}
        </button>
      </Tip>
    );
  };
  return (
    <nav className="flex w-[64px] shrink-0 flex-col items-stretch gap-1 border-r border-line bg-rail px-1.5 py-2" aria-label={t('verbs.label')} data-verb-bar>
      <div className="mb-2 flex justify-center pt-1">
        <Logo className="size-7" />
      </div>
      {verbs.map(({ verb, icon, label, hint, view: to }) => item(verb, icon, t(label), t(hint), to))}
      <div className="flex-1" />
      {item('settings', Settings, t('common.settings'), t('verbs.settings.hint'), { kind: 'settings' }, setup.data && !setup.data.ready, false)}
    </nav>
  );
}
