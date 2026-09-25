import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Ban, KeyRound, LoaderCircle, Plus, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import type * as T from '../../shared/api';
import * as A from '../../shared/api';
import { api } from '../lib/api';
import { cn, errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Input } from './ui/input';

// CredentialCard is an agent's request_credential as it stands, with what it
// takes to answer it while it is waiting (D95). The agent is blocked on it.
//
// Whatever is typed here goes to the daemon and from there into the agent's
// environment: the agent is told what happened — the account it has, the
// variable a secret is in, or that you refused — and never the value, which
// is the whole reason this is a card and not a question you answer in words.
export function CredentialCard({ question }: { question: T.Question }) {
  const waiting = question.status === 'pending' || question.status === 'escalated';
  return (
    <div className="mt-1.5 grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1.5" data-thread-credential={question.id}>
      <p className="flex items-center gap-1.5 text-[12px] font-medium text-secondary">
        <KeyRound className="size-3.5 shrink-0 text-amber-300/90" />
        <span className="min-w-0 break-words">
          {question.kind === A.CredentialSecret ? (
            <>
              Needs the secret <code className="font-mono text-[11.5px]">${question.secretName}</code>
            </>
          ) : (
            'Needs a GitHub account'
          )}
        </span>
      </p>
      <p className="whitespace-pre-wrap text-[12px] leading-relaxed text-tertiary [overflow-wrap:anywhere]">{question.question}</p>
      {question.status === 'cancelled' && (
        <p className="break-words text-[11px] text-faint" data-credential-cancelled>
          Cancelled. {question.answer || 'Nobody answered in time; the agent carried on without it.'}
        </p>
      )}
      {waiting && <CredentialAnswer question={question} />}
    </div>
  );
}

// waitingCredential is the credential request an agent is blocked on, if it
// is: the newest of its own still waiting. An agent waits on one call at a
// time, so this is the request behind the call that is spinning.
export function waitingCredential(questions: T.Question[] | undefined, agent: string): T.Question | undefined {
  return (questions ?? [])
    .filter((q) => q.agent === agent && q.kind && (q.status === 'pending' || q.status === 'escalated'))
    .toSorted((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt))[0];
}

// ChatCredentialCard is the same card in the agent's own conversation, under
// the request_credential call it is blocked on, where you are looking while
// it waits. It reads the project's questions under the rail's key, so an
// answer from either place settles both.
export function ChatCredentialCard({ agentRef }: { agentRef: string }) {
  const [project, agent] = agentRef.split('/');
  const questions = useQuery({ queryKey: ['questions', project], queryFn: () => api.questions(project) });
  const question = waitingCredential(questions.data, agent);
  if (!question) return null;
  return (
    <div className="mb-3 max-w-xl rounded-xl border border-amber-400/25 bg-amber-400/[0.04] px-3.5 py-2.5" data-chat-credential={question.id}>
      <p className="text-[10px] font-semibold uppercase tracking-[0.07em] text-amber-300/90">Waiting on you</p>
      <CredentialCard question={question} />
    </div>
  );
}

// ProjectCredentialCards are the credential requests waiting in a project,
// in its chat, where you are: the same card as the agent's thread and chat,
// under the agent that asked, and answered through the same route, so an
// answer or a cancellation anywhere settles all three.
//
// None of this is the lead's conversation. The cards are drawn from the
// project's questions, never written into the chat, and what is typed in them
// goes from here to the daemon: the lead reads neither the card nor the value.
export function ProjectCredentialCards({ project }: { project: string }) {
  const questions = useQuery({ queryKey: ['questions', project], queryFn: () => api.questions(project) });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  // One that settles while you look stays, saying how, until you close it:
  // a card that vanished as you answered it would leave you guessing.
  const [seen, setSeen] = useState<ReadonlySet<string>>(() => new Set());
  const [closed, setClosed] = useState<ReadonlySet<string>>(() => new Set());
  const requests = (questions.data ?? []).filter((q) => q.kind && (isOpen(q) || (seen.has(q.id) && !closed.has(q.id))));
  const waitingIds = requests.filter(isOpen).map((q) => q.id).join(' ');
  useEffect(() => {
    if (!waitingIds) return;
    setSeen((prev) => {
      const ids = waitingIds.split(' ').filter((id) => !prev.has(id));
      return ids.length ? new Set([...prev, ...ids]) : prev;
    });
  }, [waitingIds]);
  if (requests.length === 0) return null;
  const sorted = requests.toSorted((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
  return (
    <div className="grid gap-3 pb-4" data-project-credentials>
      {sorted.map((q) => {
        const asker = agents.data?.find((a) => a.project === q.project && a.name === q.agent);
        return (
          <div
            key={q.id}
            className={cn(
              'max-w-xl rounded-xl border px-3.5 py-2.5',
              isOpen(q) ? 'border-amber-400/25 bg-amber-400/[0.04]' : 'border-line bg-surface-faint',
            )}
            data-project-credential={q.id}
          >
            <div className="flex min-w-0 items-center gap-2">
              <p className={cn('shrink-0 text-[10px] font-semibold uppercase tracking-[0.07em]', isOpen(q) ? 'text-amber-300/90' : 'text-subtle')}>
                {isOpen(q) ? 'Waiting on you' : q.status === 'answered' ? 'Answered' : 'Cancelled'}
              </p>
              <p className="min-w-0 flex-1 truncate text-[11.5px] text-subtle" data-credential-asker={q.agent}>
                <span className="font-mono text-secondary">{q.agent}</span> asks
                {asker?.title && <span className="text-faint"> · {asker.title}</span>}
              </p>
              {!isOpen(q) && (
                <button
                  className="shrink-0 rounded p-0.5 text-faint transition hover:text-primary"
                  aria-label="Close"
                  onClick={() => setClosed((prev) => new Set([...prev, q.id]))}
                >
                  <X className="size-3.5" />
                </button>
              )}
            </div>
            <CredentialCard question={q} />
            {q.status === 'answered' && <p className="mt-1.5 text-[11px] text-faint">{answeredLine(q)}</p>}
          </div>
        );
      })}
    </div>
  );
}

function isOpen(q: T.Question): boolean {
  return q.status === 'pending' || q.status === 'escalated';
}

// answeredLine says how a request was settled without repeating what the
// agent was told, which is written for the agent.
function answeredLine(q: T.Question): string {
  if (q.answer?.startsWith('refused')) return 'You refused it.';
  return q.kind === A.CredentialSecret ? `$${q.secretName} is saved for the project's agents.` : 'The agent has a GitHub account now.';
}

function CredentialAnswer({ question }: { question: T.Question }) {
  const [refusing, setRefusing] = useState(false);
  const [reason, setReason] = useState('');
  const queryClient = useQueryClient();
  const answer = useMutation({
    mutationFn: (req: T.AnswerCredentialRequest) => api.answerCredential(question.project, question.id, req),
    // The card is in the rail and in the agent's chat at once: both read this
    // list, so the answer settles both now rather than when the event lands.
    onSuccess: (answered) =>
      queryClient.setQueryData<T.Question[]>(['questions', question.project], (list) => list?.map((q) => (q.id === answered.id ? answered : q))),
  });
  const refuse = () => answer.mutate({ refuse: true, reason: reason.trim() || undefined });

  return (
    <div className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1.5">
      {question.kind === A.CredentialSecret ? (
        <SecretForm question={question} busy={answer.isPending} onGive={(value) => answer.mutate({ value })} />
      ) : (
        <GitHubForm busy={answer.isPending} onGive={(githubAccount) => answer.mutate({ githubAccount })} />
      )}

      {refusing ? (
        <form
          className="flex min-w-0 items-center gap-1.5"
          onSubmit={(event) => {
            event.preventDefault();
            if (!answer.isPending) refuse();
          }}
        >
          <Input
            autoFocus
            aria-label="Why you refuse"
            className="h-7 min-w-0 flex-1 text-[12px]"
            placeholder="Why, for the agent (optional)"
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
          <Button type="submit" size="sm" variant="danger" disabled={answer.isPending}>
            Refuse
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setRefusing(false)}>
            Cancel
          </Button>
        </form>
      ) : (
        <Button size="sm" variant="ghost" className="justify-self-start text-subtle" onClick={() => setRefusing(true)}>
          <Ban />
          Refuse
        </Button>
      )}
      {answer.error && <p className="break-words text-[11px] text-rose-300">{errorMessage(answer.error)}</p>}
    </div>
  );
}

// SecretForm is the value, under the name the agent asked for. It is stored as
// the project's Secrets tab stores it, so every agent of the project gets it.
function SecretForm({ question, busy, onGive }: { question: T.Question; busy: boolean; onGive: (value: string) => void }) {
  const [value, setValue] = useState('');
  return (
    <form
      className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1.5"
      onSubmit={(event) => {
        event.preventDefault();
        if (value && !busy) onGive(value);
      }}
    >
      <div className="flex min-w-0 items-center gap-1.5">
        <Input
          readOnly
          aria-label="Secret name"
          className="h-7 w-[42%] min-w-0 font-mono text-[11.5px] text-subtle"
          value={question.secretName ?? ''}
        />
        <Input
          type="password"
          autoComplete="off"
          aria-label={`Value of ${question.secretName}`}
          className="h-7 min-w-0 flex-1 font-mono text-[11.5px]"
          placeholder="Value"
          value={value}
          onChange={(event) => setValue(event.target.value)}
        />
      </div>
      <p className="text-[10.5px] leading-relaxed text-faint">Saved as a project secret. The agent is told the name, never the value.</p>
      <Button type="submit" size="sm" variant="primary" className="justify-self-end" disabled={!value || busy}>
        {busy ? <LoaderCircle className="animate-spin" /> : <KeyRound />}
        Save and give
      </Button>
    </form>
  );
}

// GitHubForm picks one of this machine's GitHub accounts, or logs a new one in
// the way agentbox auth github does: a token, checked against GitHub and
// stored under a name. Either becomes the project's account and the agent's.
function GitHubForm({ busy, onGive }: { busy: boolean; onGive: (account: string) => void }) {
  const queryClient = useQueryClient();
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const accounts = auth.data?.githubAccounts ?? [];
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState('');
  const [token, setToken] = useState('');
  const login = useMutation({
    mutationFn: () => api.saveGitHubToken(token.trim(), name.trim()),
    onSuccess: async () => {
      const account = name.trim();
      setToken('');
      await queryClient.invalidateQueries({ queryKey: ['auth'] });
      onGive(account);
    },
  });

  return (
    <div className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1.5">
      {accounts.length > 0 && (
        <ul className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1" aria-label="GitHub accounts">
          {accounts.map((acc) => (
            <li key={acc.name} className="min-w-0">
              <button
                type="button"
                disabled={busy}
                data-credential-account={acc.name}
                onClick={() => onGive(acc.name)}
                className={cn(
                  'flex w-full min-w-0 items-center gap-2 rounded-md border border-line bg-surface-faint px-2 py-1 text-left transition',
                  'hover:border-line-strong hover:bg-surface-raised disabled:opacity-50',
                )}
              >
                <span className="min-w-0 shrink truncate font-mono text-[11.5px] text-secondary">{acc.name}</span>
                {acc.login && <span className="min-w-0 truncate text-[11px] text-subtle">{acc.login}</span>}
                <span className="ml-auto shrink-0 text-[10.5px] text-faint">Give</span>
              </button>
            </li>
          ))}
        </ul>
      )}
      {adding ? (
        <form
          className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1.5"
          onSubmit={(event) => {
            event.preventDefault();
            if (name.trim() && token.trim() && !login.isPending) login.mutate();
          }}
        >
          <div className="flex min-w-0 items-center gap-1.5">
            <Input
              autoFocus
              aria-label="Account name"
              className="h-7 w-[38%] min-w-0 font-mono text-[11.5px]"
              placeholder="name"
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
            <Input
              type="password"
              autoComplete="off"
              aria-label="GitHub token"
              className="h-7 min-w-0 flex-1 font-mono text-[11.5px]"
              placeholder="gh auth token, or a PAT"
              value={token}
              onChange={(event) => setToken(event.target.value)}
            />
          </div>
          <div className="flex items-center justify-end gap-1.5">
            <Button size="sm" variant="ghost" onClick={() => setAdding(false)}>
              Cancel
            </Button>
            <Button type="submit" size="sm" variant="primary" disabled={!name.trim() || !token.trim() || login.isPending || busy}>
              {login.isPending ? <LoaderCircle className="animate-spin" /> : <KeyRound />}
              Log in and give
            </Button>
          </div>
          {login.error && <p className="break-words text-[11px] text-rose-300">{errorMessage(login.error)}</p>}
        </form>
      ) : (
        <Button size="sm" variant="ghost" className="justify-self-start" onClick={() => setAdding(true)}>
          <Plus />
          Log in another account
        </Button>
      )}
      <p className="text-[10.5px] leading-relaxed text-faint">Becomes this project's GitHub account too. The agent is told which account, never the token.</p>
    </div>
  );
}
