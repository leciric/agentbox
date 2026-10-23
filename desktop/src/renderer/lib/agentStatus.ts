// The vocabulary for what an agent (or a project's agents, taken together) is
// up to right now. The rail, the sidebar, and anything else that summarizes
// agent activity should read from here rather than inventing their own words
// for the same states, so a glance at one means the same thing as a glance
// at the other.
import { useQuery } from '@tanstack/react-query';
import type * as T from '../../shared/api';
import { formatNumber, formatTime, t, type MessageKey } from '../../shared/i18n/index.ts';
import { api } from './api.ts';
import { humanBytes } from './utils.ts';

export type StatusTone = 'urgent' | 'error' | 'warning' | 'live' | 'muted';

export function chatLabel(agent: T.Agent): { text: string; tone: StatusTone } {
  // Being made comes before anything its chat says: nothing can need you yet.
  // Its whole machine paused for memory, or waiting for the memory to boot
  // (Settings → Experimental): before anything its chat says, which waits too.
  const machine = machineHold(agent);
  if (machine) return { text: machine.text, tone: 'warning' };
  if (agent.state === 'initializing' || agent.state === 'queued') return { text: t('agent.status.initializing'), tone: 'live' };
  if (agent.chat === 'waiting') return { text: t('agent.status.needsYou'), tone: 'urgent' };
  if (agent.state === 'incomplete') return { text: t('agent.status.needsAttention'), tone: 'error' };
  if (agent.state === 'missing') return { text: t('agent.status.missing'), tone: 'error' };
  // 'queued' is only ever a leftover from an earlier release, started as the
  // daemon starts: it reads like an agent that is on its way up.
  if (agent.state === 'initializing' || agent.state === 'queued') return { text: t('agent.status.initializing'), tone: 'live' };
  // A stalled turn still says running: the daemon's stall watch found no
  // progress on it for long enough that it is stuck, not working.
  if (agent.chat === 'running' && agent.stalledSince) return { text: t('agent.status.stalled'), tone: 'error' };
  // The VM is short of memory and the agent's tests or builds are held back
  // (T.Agent.memory): paused mid-run, or not yet started.
  const held = memoryHold(agent);
  if (held) return { text: held.text, tone: 'warning' };
  if (agent.chat === 'running') return { text: t('agent.status.working'), tone: 'live' };
  // Its turn ended on work it left running — a background command, a
  // monitor, a watcher — whose end wakes it to carry on. Not idle, and not
  // finished: live, so the rail keeps it out of Finished.
  if (awaiting(agent)) return { text: t('agent.status.awaiting'), tone: 'live' };
  // The AI tool exited, mid-turn or not: nothing runs until somebody sends
  // it a message, which isn't the same as idle.
  if (agent.state === 'running' && agent.chat === 'error') return { text: t('agent.status.chatStopped'), tone: 'error' };
  // A turn AgentBox's restart cut short, being carried on: its machine may
  // still be starting.
  if (agent.outcome === 'resuming' && agent.state !== 'paused') return { text: t('agent.status.resuming'), tone: 'live' };
  // Cut short by the AI tool's usage limit: paused, not finished or failed,
  // and carried on by AgentBox when the limit resets.
  if (agent.state === 'running' && agent.outcome === 'limited') return { text: limitedText(agent), tone: 'warning' };
  // How its last turn left it, as the daemon judged it (T.Agent.outcome): a
  // question in its own words waits on somebody, and a turn that died
  // mid-work isn't idle.
  if (agent.state === 'running' && agent.chat !== 'starting') {
    if (agent.outcome === 'asking') return { text: t('agent.status.asking'), tone: 'urgent' };
    if (agent.outcome === 'failed') return { text: t('agent.status.failed'), tone: 'error' };
    // Idle reclaim froze what it left running (T.Agent.reclaimed).
    if (agent.reclaimed === 'frozen') return { text: t('agent.status.reclaimed'), tone: 'muted' };
    if (agent.outcome === 'done') return { text: t('agent.status.done'), tone: 'muted' };
  }
  if (agent.state === 'running') return { text: agent.chat === 'starting' ? t('agent.status.starting') : t('agent.status.idle'), tone: 'muted' };
  if (agent.state === 'paused') return { text: t('agent.status.paused'), tone: 'muted' };
  // Stopped by idle reclaim after it finished: a message starts it again.
  if (agent.reclaimed === 'stopped') return { text: t('agent.status.reclaimed'), tone: 'muted' };
  return { text: t('agent.status.stopped'), tone: 'muted' };
}

// limitedText is "Paused: usage limit, resumes at 4:11 PM", without the time
// when AgentBox won't resume it by itself.
function limitedText(agent: Pick<T.Agent, 'outcomeUntil'>): string {
  if (!agent.outcomeUntil) return t('agent.status.limitedNoTime');
  return t('agent.status.limited', { time: formatTime(agent.outcomeUntil, { hour: '2-digit', minute: '2-digit' }) });
}

// statusTip is what chatLabel's words leave out, for a tooltip: what holds
// its commands for memory, the question it asked, the error it stopped on.
export function statusTip(agent: Pick<T.Agent, 'state' | 'chat' | 'memory' | 'outcome' | 'outcomeNote' | 'reclaimed'>): string | undefined {
  const machine = machineHold(agent);
  if (machine) return machine.tip;
  const held = memoryHold(agent);
  if (held) return held.tip;
  if (agent.chat !== 'running' && agent.reclaimed === 'frozen') return t('agent.status.reclaimedFrozenTip');
  if (agent.reclaimed === 'stopped') return t('agent.status.reclaimedStoppedTip');
  if (agent.chat === 'running' || !agent.outcomeNote) return undefined;
  if (agent.outcome === 'asking') return t('agent.status.askingTip', { question: agent.outcomeNote });
  if (agent.outcome === 'failed') return agent.outcomeNote;
  return undefined;
}

// memoryHold is what the VM's memory pressure is doing to a running agent's
// heavy commands, in words, with the sentences that explain it for a tooltip:
// what it does, then what holds them now, as the daemon's governor judged it
// (T.MemoryHold.why). undefined when nothing is held. Pausing wins over
// waiting: a paused command has already started.
export function memoryHold(agent: Pick<T.Agent, 'state' | 'memory'>): { text: string; tip: string } | undefined {
  const held = agent.memory;
  if (held?.machine) return machineHold(agent);
  if (agent.state !== 'running' || !held) return undefined;
  const why = memoryWhy(held);
  const tip = (key: MessageKey) => (why ? `${t(key)} ${why}` : t(key));
  if ((held.paused ?? 0) > 0) return { text: t('agent.status.memoryPaused'), tip: tip('agent.status.memoryPausedTip') };
  if ((held.waiting ?? 0) > 0) return { text: t('agent.status.memoryWaiting'), tip: tip('agent.status.memoryWaitingTip') };
  return undefined;
}

// machineHold is what pausing whole agents for memory (Settings →
// Experimental) does to an agent's machine: "Paused: waiting for memory, 2
// ahead" while it is frozen, "Waiting for memory, 1 ahead" while it waits to
// boot, with what holds it for a tooltip. undefined when neither.
export function machineHold(agent: Pick<T.Agent, 'memory'>): { text: string; tip: string } | undefined {
  const held = agent.memory;
  if (!held?.machine) return undefined;
  const ahead = formatNumber(held.ahead ?? 0);
  const why = memoryWhy(held);
  const tip = (key: MessageKey) => (why ? `${t(key)} ${why}` : t(key));
  if (held.machine === 'paused') return { text: t('agent.status.machinePaused', { ahead }), tip: tip('agent.status.machinePausedTip') };
  if (held.machine === 'waiting') return { text: t('agent.status.machineWaiting', { ahead }), tip: tip('agent.status.machineWaitingTip') };
  return undefined;
}

const memoryWhys: Record<string, MessageKey> = {
  stalling: 'agent.status.memoryWhy.stalling',
  falling: 'agent.status.memoryWhy.falling',
  churning: 'agent.status.memoryWhy.churning',
  low: 'agent.status.memoryWhy.low',
  easing: 'agent.status.memoryWhy.easing',
};

// memoryWhy is what holds an agent's commands now, a sentence; '' from a
// daemon too old to say.
function memoryWhy(held: T.MemoryHold): string {
  if (held.why === undefined && held.total === undefined) return '';
  const key = held.why ? memoryWhys[held.why] : 'agent.status.memoryWhy.turn';
  if (!key) return '';
  return t(key, {
    stalled: formatNumber(held.stalled ?? 0, { maximumFractionDigits: 0 }),
    rate: humanBytes(held.rate ?? 0),
    available: humanBytes(held.available ?? 0),
    total: humanBytes(held.total ?? 0),
  });
}

// awaiting is an agent between turns with background work still running
// (T.Agent.background): Claude Code wakes it when that work ends.
export function awaiting(agent: Pick<T.Agent, 'state' | 'chat' | 'background'>): boolean {
  return agent.state === 'running' && agent.chat === 'ready' && (agent.background?.length ?? 0) > 0;
}

const prStates: Record<string, MessageKey> = {
  draft: 'agent.pr.draft',
  open: 'agent.pr.open',
  merged: 'agent.pr.merged',
  closed: 'agent.pr.closed',
};
const prChecks: Record<string, MessageKey> = {
  passing: 'agent.pr.passing',
  failing: 'agent.pr.failing',
  pending: 'agent.pr.pending',
};

// prState is a pull request's state in words: draft, open, merged or closed.
export function prState(pr: Pick<T.PullRequest, 'draft' | 'state'>): string {
  const state = pr.draft ? 'draft' : pr.state;
  return prStates[state] ? t(prStates[state]) : state;
}

// prChecks is what the pull request's checks add up to, in words.
export function prChecksText(checks: string): string {
  return prChecks[checks] ? t(prChecks[checks]) : checks;
}

// usageTip is an agent's sample of CPU, memory and disk, in full, for the
// tooltip of the short form the rail and the switcher show.
export function usageTip(sample: T.AgentUsage, humanBytes: (n: number) => string, humanRate: (n: number) => string): string {
  return t('agent.usage.tip', {
    cpu: sample.cpu.toFixed(0),
    memory: humanBytes(sample.memory),
    read: humanRate(sample.diskRead),
    write: humanRate(sample.diskWrite),
  });
}

// rank sorts the agents that need you first, then whatever's running, leaving
// the ones you can safely ignore for now at the bottom. An agent still being
// made isn't waiting on you, so it doesn't jump the queue the way incomplete
// (an unfinished create with nothing left running) does.
export function rank(agent: T.Agent): number {
  if (agent.state === 'initializing' || agent.state === 'queued') return 1;
  if (agent.chat === 'waiting' || agent.state === 'incomplete' || agent.state === 'missing') return 0;
  if (agent.state === 'running' && agent.chat !== 'running' && agent.outcome === 'asking') return 0;
  if (agent.state === 'running' || agent.state === 'initializing' || agent.state === 'queued') return 1;
  if (agent.state === 'paused') return 2;
  return 3;
}

// settled is an agent with nothing going on: it isn't working, starting or
// waiting on you, and nothing about its machine needs a look — idle, paused or
// stopped. The rail folds these into its Finished section, below the ones
// that are still moving.
export function settled(agent: T.Agent): boolean {
  return chatLabel(agent).tone === 'muted' && agent.chat !== 'starting';
}

const toneOrder: StatusTone[] = ['urgent', 'error', 'warning', 'live', 'muted'];

// projectTone is the most urgent tone among a set of agents, standing in for
// all of them: the same priority the rail sorts by, collapsed to one signal
// for a place (like the project list) with no room for a row per agent.
// A machine that's merely up (state running, chat idle) doesn't count -
// that's not the same as an agent mid-turn or waiting on you.
export function projectTone(agents: T.Agent[]): StatusTone | undefined {
  let best: StatusTone | undefined;
  for (const agent of agents) {
    const tone = chatLabel(agent).tone;
    if (tone === 'muted') continue;
    if (!best || toneOrder.indexOf(tone) < toneOrder.indexOf(best)) best = tone;
  }
  return best;
}

// Mood is what an agent's avatar acts out: the same reading as chatLabel,
// cut to the five poses a face can hold.
export type Mood = 'working' | 'asking' | 'idle' | 'sleeping' | 'error';

// avatarMood reads an agent's mood from its machine and its chat. asking is
// whether it has a question or a credential request waiting on the user,
// which lives in the project's questions rather than on the agent (see
// isAsking); a permission prompt is the chat's own 'waiting'.
export function avatarMood(agent: { state: string; chat?: string; stalledSince?: string; outcome?: string }, asking = false): Mood {
  if (agent.state === 'initializing' || agent.state === 'queued') return 'sleeping';
  if (agent.state === 'incomplete' || agent.state === 'missing' || agent.chat === 'error') return 'error';
  if (agent.state === 'running' && agent.chat === 'running' && agent.stalledSince) return 'error';
  if (agent.state !== 'running') return 'sleeping';
  if (agent.chat === 'waiting' || asking) return 'asking';
  if (agent.chat === 'running' || agent.chat === 'starting') return 'working';
  if (agent.outcome === 'asking') return 'asking';
  if (agent.outcome === 'failed') return 'error';
  return 'idle';
}

// isAsking is whether ref has a question the user has to answer: a credential
// request, which only the user can, or a question the lead passed on.
export function isAsking(questions: T.Question[] | undefined, ref: string): boolean {
  return (questions ?? []).some((q) => q.ref === ref && (q.status === 'escalated' || (q.status === 'pending' && !!q.kind)));
}

// summarizeStatus is a one-line breakdown of what a set of agents is up to,
// grouped by chatLabel's own text and ordered by urgency, so whatever needs
// you is the first thing shown. Home's StatusSummary reads this.
export function summarizeStatus(agents: T.Agent[]): { text: string; tone: StatusTone; count: number }[] {
  const counts = new Map<string, { tone: StatusTone; count: number }>();
  for (const agent of agents) {
    const { text, tone } = chatLabel(agent);
    const entry = counts.get(text);
    if (entry) entry.count++;
    else counts.set(text, { tone, count: 1 });
  }
  return Array.from(counts, ([text, v]) => ({ text, ...v })).sort((a, b) => toneOrder.indexOf(a.tone) - toneOrder.indexOf(b.tone));
}

// useMood is avatarMood with the asking read from the agent's project: the
// same ['questions', project] query the rail and the credential cards read,
// so the rows of one project share one request.
export function useMood(agent: { project: string; ref: string; state: string; chat?: string; stalledSince?: string; outcome?: string }): Mood {
  const questions = useQuery({ queryKey: ['questions', agent.project], queryFn: () => api.questions(agent.project) });
  return avatarMood(agent, isAsking(questions.data, agent.ref));
}
