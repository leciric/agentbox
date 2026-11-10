// AgentMonitor is the small monitor an agent's card in the panel leads with
// (AgentList's AgentLine), a screen on a stand acting out what the agent is
// doing (shared/monitor.ts monitorScreen): working, its AI tool's mark with a
// cursor typing lines of light, which scroll at a pace set by its recent tool
// calls (typingPace); needing you, a "?" popping from the screen and an amber
// glow on the frame; failed, a red screen that glitches with an ✕; ready to
// review, a green ✓; idle or stopped, a dim screen, the mark faded and a "z"
// floating up; using its desktop, that desktop, live, in the screen (desktop).
// tiny is the folded strip's (AgentStrip): the same frame and states round the
// agent's initials (label), with no lines and no desktop.
//
// The motion is styles.css's .ab-monitor: transform and opacity only, none
// under prefers-reduced-motion, where every screen holds a still that still
// reads, and paused while the card is off screen (paused).
import type { CSSProperties, ReactNode } from 'react';
import { Check, X } from 'lucide-react';
import type { MonitorScreen } from '../../../shared/monitor';
import type { Mood } from '../../lib/agentStatus';
import { cn } from '../../lib/utils';
import { AgentCharacter, hasCharacter } from '../AgentCharacter';
import { ToolMark } from '../state';

// screenMood is the pose the mark holds on each screen.
const screenMood: Record<MonitorScreen, Mood> = { working: 'idle', needs: 'asking', error: 'error', review: 'idle', idle: 'sleeping', desktop: 'idle' };

function Mark({ ai, screen, className }: { ai: string; screen: MonitorScreen; className?: string }) {
  if (hasCharacter(ai)) return <AgentCharacter ai={ai} mood={screenMood[screen]} className={cn('ab-monitor-mark block shrink-0', className)} />;
  return <ToolMark ai={ai} className={cn('ab-monitor-mark text-brand-300', className)} />;
}

// Lines are a working screen's lines of light: a column of bars that steps up
// one line per pace, beneath one that types at the bottom with the cursor.
// The column holds its four bars twice, so its loop ends where it starts.
const widths = ['78%', '52%', '90%', '64%'];
function Lines() {
  return (
    <span className="ab-monitor-lines relative min-w-0 flex-1 self-stretch overflow-hidden" data-monitor-lines>
      <span className="ab-monitor-scroll absolute inset-x-0 bottom-[11px] flex flex-col gap-[4px]">
        {[...widths, ...widths].map((w, i) => (
          <span key={i} className="block h-[3px] shrink-0 rounded-full bg-brand-400/45" style={{ width: w }} />
        ))}
      </span>
      <span className="absolute inset-x-0 bottom-[2px] h-[5px]">
        <span className="ab-monitor-type absolute left-0 top-px block h-[3px] w-[70%] rounded-full bg-brand-300/80" />
        <span className="ab-monitor-caret absolute left-0 top-0 block h-[5px] w-[70%]">
          <span className="ab-monitor-cursor absolute -right-[4px] top-0 block h-[5px] w-[3px] bg-[var(--ab-term-foreground)]" />
        </span>
      </span>
    </span>
  );
}

export function AgentMonitor({ ai, screen, pace = 3.2, paused, tiny, label, desktop }: { ai: string; screen: MonitorScreen; pace?: number; paused?: boolean; tiny?: boolean; label?: string; desktop?: ReactNode }) {
  const glyph = tiny ? 'size-2.5' : 'size-5';
  let content: ReactNode;
  if (screen === 'desktop' && desktop && !tiny) {
    content = (
      <span className="row-swap-in block size-full" data-list-desktop>
        {desktop}
      </span>
    );
  } else if (tiny) {
    content = (
      <span className="flex items-baseline text-[10px] font-semibold leading-none tabular-nums">
        <span className="ab-monitor-label">{label}</span>
        {screen === 'working' && <span className="ab-monitor-cursor ml-px inline-block h-[7px] w-[2px] bg-[var(--ab-term-foreground)]" />}
      </span>
    );
  } else if (screen === 'working') {
    content = (
      <span className="flex size-full items-center gap-2 px-2.5 py-2">
        <Mark ai={ai} screen={screen} className="size-6" />
        <Lines />
      </span>
    );
  } else if (screen === 'error') {
    content = (
      <span className="ab-monitor-glitch flex size-full items-center justify-center gap-1.5">
        <Mark ai={ai} screen={screen} className="size-5 opacity-60" />
        <X className={cn(glyph, 'text-rose-300')} strokeWidth={3} />
      </span>
    );
  } else if (screen === 'review') {
    content = (
      <span className="flex size-full items-center justify-center gap-1.5">
        <Mark ai={ai} screen={screen} className="size-5 opacity-70" />
        <Check className={cn('ab-monitor-pop', glyph, 'text-emerald-300')} strokeWidth={3} />
      </span>
    );
  } else {
    content = (
      <span className="flex size-full items-center justify-center">
        <Mark ai={ai} screen={screen} className={cn('size-7', screen === 'idle' && 'opacity-40 grayscale')} />
      </span>
    );
  }
  return (
    <span
      aria-hidden
      className={cn('ab-monitor relative flex shrink-0 flex-col items-center', tiny ? 'w-8' : 'w-28')}
      style={{ '--ab-pace': `${pace}s` } as CSSProperties}
      data-monitor={screen}
      data-monitor-tiny={tiny || undefined}
      data-paused={paused || undefined}
    >
      <span
        className={cn(
          'ab-monitor-frame relative block w-full border bg-surface-strong transition-[border-color,box-shadow] duration-300',
          tiny ? 'rounded-[4px] p-px' : 'rounded-md p-[3px]',
          screen === 'needs' ? 'border-amber-400/70' : screen === 'error' ? 'border-rose-400/60' : screen === 'review' ? 'border-emerald-400/45' : 'border-line-strong group-hover:border-brand-500/60',
        )}
      >
        {screen === 'needs' && <span className={cn('ab-monitor-glow pointer-events-none absolute inset-0 rounded-[inherit]', tiny ? 'shadow-[0_0_6px_0_var(--ab-amber-400)]' : 'shadow-[0_0_12px_1px_var(--ab-amber-400)]')} />}
        <span
          className={cn(
            'relative flex items-center justify-center overflow-hidden text-[var(--ab-term-foreground)] transition-[background-color,opacity] duration-300',
            tiny ? 'h-[18px] rounded-[2px]' : 'aspect-[16/9] rounded-[3px]',
            // An idle screen is off: the terminal's ground with some of its ink in it,
            // greyer than a working one's in either theme.
            screen === 'desktop' && !tiny ? 'bg-stage' : screen === 'error' ? 'bg-rose-400/25' : screen === 'review' ? 'bg-emerald-400/10' : screen === 'idle' ? 'bg-[color-mix(in_srgb,var(--ab-term-background)_86%,var(--ab-term-foreground))] opacity-70' : 'bg-terminal',
          )}
          data-monitor-screen
        >
          {content}
          {screen === 'idle' && (
            <span className={cn('ab-monitor-z pointer-events-none absolute font-bold leading-none text-muted', tiny ? 'right-px top-0 text-[8px]' : 'right-2 top-1 text-[13px]')}>z</span>
          )}
          {screen === 'desktop' && !tiny && (
            <span className="absolute right-1 top-1 flex size-4 items-center justify-center rounded bg-sunken/85">
              <Mark ai={ai} screen={screen} className="size-3" />
            </span>
          )}
          {screen === 'desktop' && <span className={cn('absolute rounded-full bg-emerald-400', tiny ? 'bottom-px right-px size-1' : 'left-1 top-1 size-1.5')} />}
        </span>
        {screen === 'needs' && (
          <span className={cn('ab-monitor-ask absolute flex items-center justify-center rounded-full border border-amber-400/70 bg-sunken font-bold leading-none text-amber-300 shadow-sm', tiny ? '-right-1 -top-1 size-3 text-[8px]' : '-right-1.5 -top-2 size-5 text-[12px]')}>
            ?
          </span>
        )}
        {tiny && (screen === 'error' || screen === 'review') && (
          <span className={cn('ab-monitor-pop absolute -right-1 -top-1 flex size-3 items-center justify-center rounded-full bg-sunken', screen === 'error' ? 'text-rose-300' : 'text-emerald-300')}>
            {screen === 'error' ? <X className="size-2.5" strokeWidth={3.5} /> : <Check className="size-2.5" strokeWidth={3.5} />}
          </span>
        )}
      </span>
      <span className={cn('block bg-ghost', tiny ? 'h-[3px] w-1.5' : 'h-[5px] w-4')} />
      <span className={cn('block rounded-full bg-ghost', tiny ? 'h-[2px] w-4' : 'h-[3px] w-10')} />
    </span>
  );
}
