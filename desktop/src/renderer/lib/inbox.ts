// The top bar's inbox (components/Inbox.tsx): one button for what needs the
// user, across projects, in place of a bell and a "Ready to review" button
// side by side. Its badge counts what waits on somebody — agents ready to
// review, questions, permission requests and agents stopped on an error. What
// agents merely did, such as finishing, isn't in it: finished work is in the
// review list. The lists come a page at a time (lib/inboxPages.ts), so the
// counts are the lists' totals, not what's loaded.
import type * as T from '../../shared/api';
import { chatLabel } from './agentStatus.ts';

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
}: {
  agents: readonly T.Agent[];
  questions: readonly T.Notification[];
  review: readonly T.ReviewItem[];
  totals: { questions: number; review: number };
}): NeedsYou {
  const reviewing = new Set(review.map((i) => i.ref));
  const permissions = agents.filter((a) => a.chat === 'waiting');
  const asking = new Set(permissions.map((a) => a.ref));
  // An agent that failed and finished is in review already, with its outcome.
  const errors = agents.filter((a) => !asking.has(a.ref) && !reviewing.has(a.ref) && chatLabel(a).tone === 'error');
  const count = permissions.length + Math.max(totals.questions, questions.length) + errors.length + Math.max(totals.review, review.length);
  return { permissions, questions: [...questions], errors, review: [...review], count };
}
