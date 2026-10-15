// The top bar's inbox (components/Inbox.tsx): one button for what needs the
// user, across projects, in place of a bell and a "Ready to review" button
// side by side. Its badge counts what waits on somebody — agents ready to
// unseen review, questions, permission requests and agents stopped on an error. What
// agents merely did, such as finishing, isn't in it: finished work is in the
// review list. The lists come a page at a time (lib/inboxPages.ts), so the
// counts are the lists' totals, not what's loaded.
import type * as T from '../../shared/api';
import { chatLabel } from './agentStatus.ts';
import { readyKey } from './reviewAlert.ts';

export type NeedsYou = {
  // Agents whose AI tool asks to be allowed to do something.
  permissions: T.Agent[];
  // Questions (agentbox ask) not seen yet, as many as are loaded.
  questions: T.Notification[];
  // Agents stopped on an error, stalled or lost, that aren't in review.
  errors: T.Agent[];
  // Agents ready to review, as many as are loaded.
  review: T.ReviewItem[];
  // Everything that waits on you, each agent once, whether loaded or not.
  count: number;
};

// totals are how many questions are unseen and agents ready to review in all,
// from the lists' pages.
export function needsYou({
  agents,
  questions,
  review,
  totals,
  seen,
}: {
  agents: readonly T.Agent[];
  questions: readonly T.Notification[];
  review: readonly T.ReviewItem[];
  totals: { questions: number; review: number };
  // Finishes (readyKey) already opened: they stay in the list but not the count.
  seen?: ReadonlySet<string>;
}): NeedsYou {
  const reviewing = new Set(review.map((i) => i.ref));
  const permissions = agents.filter((a) => a.chat === 'waiting');
  const asking = new Set(permissions.map((a) => a.ref));
  // An agent that failed and finished is in review already, with its outcome.
  const errors = agents.filter((a) => !asking.has(a.ref) && !reviewing.has(a.ref) && chatLabel(a).tone === 'error');
  const count =
    permissions.length +
    Math.max(totals.questions, questions.length) +
    errors.length +
    Math.max(0, Math.max(totals.review, review.length) - (seen ? review.filter((i) => seen.has(readyKey(i))).length : 0));
  return {
    permissions,
    questions: [...questions],
    errors,
    review: [...review],
    count,
  };
}

// NeedRow is one thing waiting on the user, whatever it is, in the one shape
// the inbox draws every "Needs you" row from.
export type NeedRow = {
  key: string;
  kind: 'permission' | 'question' | 'error';
  ref: string;
  project: string;
  // The agent's title, or its name when it has none.
  title: string;
  // What it asks (a question's text) or stopped on (the error), when it says.
  detail?: string;
  // When it began, if known: agents carry no time for a permission request.
  at?: string;
  // The question's notification, which opens through its own handler.
  notice?: T.Notification;
};

// needRows lists what needs you in one order: permission requests (the agent
// is blocked, live), then questions newest first, then errors.
export function needRows(needs: Pick<NeedsYou, 'permissions' | 'questions' | 'errors'>, agents: readonly T.Agent[]): NeedRow[] {
  const byRef = new Map(agents.map((a) => [a.ref, a]));
  const ofAgent = (kind: 'permission' | 'error', a: T.Agent): NeedRow => ({
    key: `${kind}:${a.ref}`,
    kind,
    ref: a.ref,
    project: a.project,
    title: a.title || a.name,
    detail: kind === 'error' ? a.outcomeNote || chatLabel(a).text : undefined,
    at: kind === 'error' ? a.stalledSince : undefined,
  });
  const questions = [...needs.questions]
    .sort((a, b) => b.at.localeCompare(a.at))
    .map((n): NeedRow => ({
      key: `question:${n.id}`,
      kind: 'question',
      ref: n.ref,
      project: n.project,
      title: n.title || byRef.get(n.ref)?.title || n.agent,
      detail: n.text,
      at: n.at,
      notice: n,
    }));
  return [...needs.permissions.map((a) => ofAgent('permission', a)), ...questions, ...needs.errors.map((a) => ofAgent('error', a))];
}
