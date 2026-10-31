// The tiny monitor each agent's card leads with (renderer/components/v2/
// AgentMonitor.tsx on the desktop, ui/icons.tsx's AgentMonitor on the phone):
// which screen it shows for the agent's state, and how fast a working agent's
// lines of light scroll. Shared, so the two apps act an agent out alike.

// MonitorSt is an agent's state as the lists group it (renderer/lib/
// leadFirst.ts AgentSt): what needs you, failed, ready to review, working and
// done, which is idle or stopped.
export type MonitorSt = 'needs' | 'error' | 'review' | 'working' | 'done';

export type MonitorScreen = 'working' | 'needs' | 'error' | 'review' | 'idle' | 'desktop';

// monitorScreen is what the screen shows: the agent's live desktop while it
// uses it and its machine runs, else its state. What needs you, or failed,
// wins over the desktop: the screen shouldn't hide that.
export function monitorScreen(st: MonitorSt, { live = false, running = true }: { live?: boolean; running?: boolean } = {}): MonitorScreen {
  if (st === 'needs' || st === 'error') return st;
  if (live && running) return 'desktop';
  if (st === 'done') return 'idle';
  return st;
}

// paceWindow is how far back typingPace counts tool calls.
export const paceWindow = 120_000;

// typingPace is how many seconds one line of light takes to type and scroll
// on a working agent's screen: slow (3.2s) with no tool call in the last two
// minutes, quicker with each, down to 0.8s at twenty or more. calls are the
// tool calls' start times (ISO); unreadable ones are skipped.
export function typingPace(calls: readonly string[], now = Date.now()): number {
  let n = 0;
  for (const at of calls) {
    const t = new Date(at).getTime();
    if (Number.isFinite(t) && t <= now && now - t <= paceWindow) n++;
  }
  return Math.round(Math.max(0.8, 3.2 - n * 0.12) * 100) / 100;
}

// moodSt is MonitorSt from an agent's avatar mood (the phone's avatarMood,
// which has no review): ready to review when its work waits in the review,
// as the desktop's lists read it, unless it asks for you.
export function moodSt(mood: 'working' | 'idle' | 'asking' | 'sleeping' | 'error', review = false): MonitorSt {
  if (mood === 'asking') return 'needs';
  if (review && mood !== 'working') return 'review';
  if (mood === 'error') return 'error';
  if (mood === 'working') return 'working';
  return 'done';
}
