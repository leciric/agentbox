// The top bar's inbox (components/Inbox.tsx): one button for what needs the
// user, across projects, in place of a bell and a "Ready to review" button
// side by side. Its badge counts what waits on somebody — agents ready to
// review, questions, permission requests and agents stopped on an error —
// and the rest of the notifications (a finish, a screenshot kept) only mark
// it as having news.
import type * as T from '../../shared/api';
import { chatLabel } from './agentStatus.ts';

export type NeedsYou = {
  // Agents whose AI tool asks to be allowed to do something.
  permissions: T.Agent[];
  // Questions (agentbox ask) not seen yet.
  questions: T.Notification[];
  // Agents stopped on an error, stalled or lost, that aren't in review.
  errors: T.Agent[];
  // Agents ready to review.
  review: T.ReviewItem[];
  // All of them, each agent once.
  count: number;
  // Notifications not seen yet that don't wait on anybody.
  news: number;
};

export function needsYou({ agents, notices, review }: { agents: readonly T.Agent[]; notices: readonly T.Notification[]; review: readonly T.ReviewItem[] }): NeedsYou {
  const reviewing = new Set(review.map((i) => i.ref));
  const permissions = agents.filter((a) => a.chat === 'waiting');
  const asking = new Set(permissions.map((a) => a.ref));
  // An agent that failed and finished is in review already, with its outcome.
  const errors = agents.filter((a) => !asking.has(a.ref) && !reviewing.has(a.ref) && chatLabel(a).tone === 'error');
  const questions = notices.filter((n) => n.kind === 'question' && !n.seen);
  const news = notices.filter((n) => n.kind !== 'question' && !n.seen).length;
  return { permissions, questions, errors, review: [...review], count: permissions.length + questions.length + errors.length + review.length, news };
}
