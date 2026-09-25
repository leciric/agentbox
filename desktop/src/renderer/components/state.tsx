import { Bot, LoaderCircle, Sparkles, SquareTerminal, Terminal } from 'lucide-react';
import { type Mood, useMood } from '../lib/agentStatus';
import { cn } from '../lib/utils';
import { AgentCharacter, hasCharacter } from './AgentCharacter';
import { Badge, type BadgeVariant } from './ui/badge';

const agentVariants: Record<string, BadgeVariant> = {
  running: 'success',
  paused: 'warning',
  stopped: 'default',
  initializing: 'info',
  incomplete: 'danger',
  missing: 'danger',
};

const dotColors: Record<string, string> = {
  running: 'bg-emerald-400',
  paused: 'bg-amber-400',
  stopped: 'bg-faint',
  initializing: 'bg-sky-400',
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

// AgentAvatar is an agent's AI tool in a tile: its mark as a small character
// acting out the agent's mood (avatarMood), on a tile tinted by it — amber
// when it's waiting on you, rose when something broke, grey when its machine
// is off. Every tint is a layer of its own, faded in and out, so a change of
// mood eases over rather than swapping (a gradient can't transition).
// seed spreads the animations of a column of them out (an agent's ref does).
const tints = {
  brand: 'from-brand-500/20 via-indigo-500/[0.07] to-transparent',
  quiet: 'from-surface-raised via-transparent to-transparent',
  amber: 'from-amber-400/20 via-amber-400/[0.05] to-transparent',
  rose: 'from-rose-500/[0.18] via-transparent to-transparent',
};
const moodTint: Record<Mood, keyof typeof tints> = { working: 'brand', idle: 'brand', asking: 'amber', sleeping: 'quiet', error: 'rose' };

export function AgentAvatar({ ai, mood, state, seed, className }: { ai: string; mood: Mood; state?: string; seed?: string; className?: string }) {
  return (
    <span
      data-mood={mood}
      className={cn(
        'relative flex size-10 shrink-0 [container-type:size] items-center justify-center rounded-xl border border-line-strong text-brand-200 transition-[border-color,box-shadow,color] duration-300',
        (mood === 'sleeping' || mood === 'error') && 'text-muted',
        mood === 'asking' && 'border-amber-400/45 shadow-[0_0_10px_-5px_rgb(251_191_36/0.55)]',
        mood === 'error' && 'border-rose-400/40',
        className,
      )}
    >
      {Object.entries(tints).map(([name, tint]) => (
        <span
          key={name}
          aria-hidden
          className={cn('absolute inset-0 rounded-[inherit] bg-gradient-to-br transition-opacity duration-300', tint, moodTint[mood] === name ? 'opacity-100' : 'opacity-0')}
        />
      ))}
      {hasCharacter(ai) ? (
        <AgentCharacter
          ai={ai}
          mood={mood}
          seed={seed}
          className={cn('size-[58%]', mood === 'sleeping' && 'opacity-45 grayscale')}
        />
      ) : (
        <AIIcon ai={ai} className="relative size-4" />
      )}
      <span aria-hidden className={cn('ab-zzz pointer-events-none absolute inset-0 transition-opacity duration-300', mood === 'sleeping' ? 'opacity-100' : 'opacity-0')}>
        <span className="right-[16%] top-[30%] text-[14cqw]">z</span>
        <span className="right-[9%] top-[17%] text-[16cqw]">z</span>
        <span className="right-[2%] top-[4%] text-[19cqw]">z</span>
      </span>
      {state && <StatusDot state={state} className="absolute -bottom-0.5 -right-0.5 size-2.5 ring-2 ring-ink" />}
    </span>
  );
}

// LiveAgentAvatar is AgentAvatar for one agent, reading its mood itself.
export function LiveAgentAvatar({ agent, className }: { agent: { ai: string; project: string; ref: string; state: string; chat?: string }; className?: string }) {
  return <AgentAvatar ai={agent.ai} mood={useMood(agent)} state={agent.state} seed={agent.ref} className={className} />;
}
