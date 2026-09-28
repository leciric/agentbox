import { useQuery } from '@tanstack/react-query';
import { MemoryStick } from 'lucide-react';
import { api } from '../lib/api';
import { cn, humanBytes } from '../lib/utils';
import { Button } from './ui/button';

// BudgetShortageWarning is the shared budget's own warning, at the top of the
// rail: the agents in it thrashing at its memory together (agent.ThrashWatch's
// group), which no agent's own check catches — each may be well under the bar
// alone, with no limit of its own. It comes with Settings, where
// EventBudget has the app read it again when it starts or stops. There's no
// one-click raise: whatever the agents get is taken from what's reserved for
// the host's own apps, so how much to give up is Settings' question.
export function BudgetShortageWarning({ onOpenSettings, className }: { onOpenSettings: () => void; className?: string }) {
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const b = settings.data?.sharedBudget;
  const short = b?.shortage;
  if (!b || !short) return null;

  return (
    <div
      className={cn('grid min-w-0 gap-1.5 rounded-lg border border-amber-400/25 bg-amber-400/[0.08] px-2.5 py-2 text-[11.5px]', className)}
      data-budget-shortage
      role="alert"
    >
      <span className="flex min-w-0 items-center gap-1.5 font-medium text-amber-300">
        <MemoryStick className="size-3.5 shrink-0" />
        <span className="min-w-0">The agents are short of memory together</span>
      </span>
      <span className="leading-snug text-secondary">
        {`Held at the shared budget's ${humanBytes(short.limit)}, they're re-reading ${humanBytes(short.refaultRate)}/s from disk, and waiting on memory ${Math.round(short.pressure)}% of the time.`}
      </span>
      <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <Button variant="default" className="h-7 shrink-0 px-2.5 text-[12px]" onClick={onOpenSettings}>
          Resize the budget
        </Button>
        <span className="min-w-0 leading-snug text-subtle">or stop an agent you don't need.</span>
      </span>
    </div>
  );
}
