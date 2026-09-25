// The vocabulary for what an agent (or a project's agents, taken together) is
// up to right now. The rail, the sidebar, and anything else that summarizes
// agent activity should read from here rather than inventing their own words
// for the same states, so a glance at one means the same thing as a glance
// at the other.
import { useQuery } from '@tanstack/react-query';
import type * as T from '../../shared/api';
import { api } from './api';

export type StatusTone = 'urgent' | 'error' | 'live' | 'muted';

export function chatLabel(agent: T.Agent): { text: string; tone: StatusTone } {
  if (agent.chat === 'waiting') return { text: 'Needs you', tone: 'urgent' };
  if (agent.state === 'incomplete') return { text: 'Needs attention', tone: 'error' };
  if (agent.state === 'missing') return { text: 'Missing', tone: 'error' };
  if (agent.chat === 'running') return { text: 'Working', tone: 'live' };
  if (agent.state === 'running') return { text: agent.chat === 'starting' ? 'Starting' : 'Idle', tone: 'muted' };
  if (agent.state === 'paused') return { text: 'Paused', tone: 'muted' };
  return { text: 'Stopped', tone: 'muted' };
}

// rank sorts the agents that need you first, then whatever's running, leaving
// the ones you can safely ignore for now at the bottom.
export function rank(agent: T.Agent): number {
  if (agent.chat === 'waiting' || agent.state === 'incomplete' || agent.state === 'missing') return 0;
  if (agent.state === 'running') return 1;
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

const toneOrder: StatusTone[] = ['urgent', 'error', 'live', 'muted'];

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
export function avatarMood(agent: { state: string; chat?: string }, asking = false): Mood {
  if (agent.state === 'incomplete' || agent.state === 'missing' || agent.chat === 'error') return 'error';
  if (agent.state !== 'running') return 'sleeping';
  if (agent.chat === 'waiting' || asking) return 'asking';
  if (agent.chat === 'running' || agent.chat === 'starting') return 'working';
  return 'idle';
}

// isAsking is whether ref has a question the user has to answer: a credential
// request, which only the user can, or a question the lead passed on.
export function isAsking(questions: T.Question[] | undefined, ref: string): boolean {
  return (questions ?? []).some((q) => q.ref === ref && (q.status === 'escalated' || (q.status === 'pending' && !!q.kind)));
}

// useMood is avatarMood with the asking read from the agent's project: the
// same ['questions', project] query the rail and the credential cards read,
// so the rows of one project share one request.
export function useMood(agent: { project: string; ref: string; state: string; chat?: string }): Mood {
  const questions = useQuery({ queryKey: ['questions', agent.project], queryFn: () => api.questions(agent.project) });
  return avatarMood(agent, isAsking(questions.data, agent.ref));
}
