import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Ban, KeyRound, LoaderCircle, Plus } from 'lucide-react';
import { useState } from 'react';
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
      {question.status === 'cancelled' && <p className="text-[11px] text-faint">Nobody answered in time; the agent carried on without it.</p>}
      {waiting && <CredentialAnswer question={question} />}
    </div>
  );
}

function CredentialAnswer({ question }: { question: T.Question }) {
  const [refusing, setRefusing] = useState(false);
  const [reason, setReason] = useState('');
  const answer = useMutation({
    mutationFn: (req: T.AnswerCredentialRequest) => api.answerCredential(question.project, question.id, req),
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
