import { useMutation, useQueryClient } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';

// What CredentialCard and ConnectorRequestCard share: answering one request
// or several asking for the same thing, and saying why each agent asked.

// useAnswerRequests answers every request in a group with the same answer,
// one after another. The card is in the project's chat and the agents' own at
// once: each answer goes into the list they all read as it comes back, so a
// group settles there before the events land, and one that fails part-way
// still shows which went through.
export function useAnswerRequests(questions: readonly T.Question[]) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (req: T.AnswerCredentialRequest) => {
      const answered: T.Question[] = [];
      for (const q of questions) {
        const a = await api.answerCredential(q.project, q.id, req);
        queryClient.setQueryData<T.Question[]>(['questions', a.project], (list) => list?.map((x) => (x.id === a.id ? a : x)));
        answered.push(a);
      }
      return answered;
    },
  });
}

// RequestReasons is what each agent said it needs it for: under its name when
// several asked, since one answer goes to all of them.
export function RequestReasons({ group }: { group: readonly T.Question[] }) {
  const t = useT();
  const text = 'whitespace-pre-wrap text-ui leading-relaxed text-tertiary [overflow-wrap:anywhere]';
  if (group.length === 1) return <p className={text}>{group[0].question}</p>;
  return (
    <div className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1.5" data-request-reasons>
      <p className="text-caption text-subtle">{t('chat.request.oneAnswer', { count: group.length })}</p>
      {group.map((q) => (
        <p key={q.id} className={text}>
          <span className="font-mono text-caption text-secondary">{q.agent}</span> {q.question}
        </p>
      ))}
    </div>
  );
}

// WaitingLabel is the one amber thing on a request: a dot and "Waiting on
// you", in the sentence case the rest of the card is in.
export function WaitingLabel({ children }: { children?: ReactNode }) {
  const t = useT();
  return (
    <p className="flex min-w-0 items-center gap-1.5 text-caption text-subtle">
      <span aria-hidden className="size-1.5 shrink-0 rounded-full bg-amber-400" />
      <span className="shrink-0 font-medium text-amber-300 [:root[data-appearance=light]_&]:text-amber-700">{t('chat.credential.waitingOnYou')}</span>
      {children}
    </p>
  );
}
