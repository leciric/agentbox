import { Bot, LoaderCircle, Sparkles, SquareTerminal, Terminal } from 'lucide-react';
import { useMood, type Mood } from '../lib/agentStatus';
import { cn } from '../lib/utils';
import { AgentCharacter, hasCharacter } from './AgentCharacter';
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

// AgentAvatar is an agent's AI tool in a tile: its mark as a character acting
// out the agent's mood (avatarMood), on a tile tinted by it — amber when it's
// waiting on you, rose when something broke, grey when its machine is off.
// seed spreads the animations of a column of them out (an agent's ref does).
export function AgentAvatar({ ai, mood, state, seed, className }: { ai: string; mood: Mood; state?: string; seed?: string; className?: string }) {
  return (
    <span
      data-mood={mood}
      className={cn(
        'relative flex size-10 shrink-0 [container-type:size] items-center justify-center rounded-xl border border-line-strong bg-gradient-to-br text-brand-200',
        mood === 'working' || mood === 'idle' ? 'from-brand-500/25 via-indigo-500/10 to-transparent' : 'from-surface-raised via-transparent to-transparent text-muted',
        mood === 'asking' && 'border-amber-400/70 from-amber-400/30 shadow-[0_0_14px_-4px_rgb(251_191_36/0.7)]',
        mood === 'error' && 'border-rose-400/60 from-rose-500/25',
        className,
      )}
    >
      {hasCharacter(ai) ? (
        <AgentCharacter ai={ai} mood={mood} seed={seed} className={cn('size-[76%]', mood === 'sleeping' && 'opacity-50 grayscale')} />
      ) : (
        <AIIcon ai={ai} className="size-5" />
      )}
      {mood === 'sleeping' && (
        <span aria-hidden className="ab-zzz pointer-events-none absolute inset-0">
          <span className="right-[14%] top-[34%] text-[18cqw]">z</span>
          <span className="right-[6%] top-[18%] text-[21cqw]">z</span>
          <span className="-right-[2%] top-[2%] text-[25cqw]">z</span>
        </span>
      )}
      {state && <StatusDot state={state} className="absolute -bottom-0.5 -right-0.5 size-2.5 ring-2 ring-ink" />}
    </span>
  );
}

// LiveAgentAvatar is AgentAvatar for one agent, reading its mood itself.
export function LiveAgentAvatar({ agent, className }: { agent: { ai: string; project: string; ref: string; state: string; chat?: string }; className?: string }) {
  return <AgentAvatar ai={agent.ai} mood={useMood(agent)} state={agent.state} seed={agent.ref} className={className} />;
}
