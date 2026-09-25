import { Bot, LoaderCircle, Sparkles, SquareTerminal, Terminal } from 'lucide-react';
import { cn } from '../lib/utils';
import { Badge, type BadgeVariant } from './ui/badge';

const agentVariants: Record<string, BadgeVariant> = {
  running: 'success',
  paused: 'warning',
  stopped: 'default',
  incomplete: 'danger',
  missing: 'danger',
};

const dotColors: Record<string, string> = {
  running: 'bg-emerald-400',
  paused: 'bg-amber-400',
  stopped: 'bg-faint',
  incomplete: 'bg-rose-400',
  missing: 'bg-rose-400',
};

export function StatusDot({ state, className }: { state: string; className?: string }) {
  return (
    <span
      title={state}
      className={cn('inline-block size-2 shrink-0 rounded-full', dotColors[state] ?? 'bg-faint', state === 'running' && 'animate-glow', className)}
    />
  );
}

export function StateBadge({ state }: { state: string }) {
  return (
    <Badge variant={agentVariants[state] ?? 'default'} data-state={state}>
      <StatusDot state={state} className="size-1.5" />
      {state}
    </Badge>
  );
}

const jobVariants: Record<string, BadgeVariant> = {
  running: 'info',
  succeeded: 'success',
  failed: 'danger',
  cancelled: 'warning',
};

export function JobStatusBadge({ status }: { status: string }) {
  return (
    <Badge variant={jobVariants[status] ?? 'default'} data-job-status={status}>
      {status === 'running' && <LoaderCircle className="animate-spin" />}
      {status}
    </Badge>
  );
}

const aiLabels: Record<string, string> = { claude: 'Claude Code', codex: 'Codex', opencode: 'OpenCode' };

export function aiLabel(ai: string): string {
  return aiLabels[ai] ?? 'Shell only';
}

export function AIIcon({ ai, className }: { ai: string; className?: string }) {
  const Icon = ai === 'claude' ? Sparkles : ai === 'codex' ? Bot : ai === 'opencode' ? Terminal : SquareTerminal;
  return <Icon className={className} />;
}

// AgentAvatar is an agent's AI tool in a tile, tinted by its state.
export function AgentAvatar({ ai, state, className }: { ai: string; state: string; className?: string }) {
  return (
    <span
      className={cn(
        'relative flex size-11 shrink-0 items-center justify-center rounded-xl border border-line-strong bg-gradient-to-br from-brand-500/25 via-indigo-500/10 to-transparent text-brand-200',
        state !== 'running' && 'from-surface-raised via-transparent text-muted',
        className,
      )}
    >
      <AIIcon ai={ai} className="size-5" />
      <StatusDot state={state} className="absolute -bottom-0.5 -right-0.5 size-2.5 ring-2 ring-ink" />
    </span>
  );
}
