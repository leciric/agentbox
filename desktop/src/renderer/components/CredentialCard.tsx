import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Ban, ChevronUp, KeyRound, LoaderCircle, Plug, Plus, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import type * as T from '../../shared/api';
import * as A from '../../shared/api';
import { api } from '../lib/api';
import { connectorRequest, RequestConnector } from '../lib/connectors';
import { groupRequests, isOpenRequest as isOpen } from '../lib/credentialRequests';
import { formatList, t, type Translate, useT } from '../lib/i18n';
import { cn, errorMessage } from '../lib/utils';
import { ConnectorRequestCard } from './ConnectorRequestCard';
import { RequestReasons, useAnswerRequests, WaitingLabel } from './RequestParts';
import { AIToolSecretWarning } from './ToolCredential';
import { Button } from './ui/button';
import { Input } from './ui/input';

// CredentialCard is an agent's request_credential as it stands, with what it
// takes to answer it while it is waiting (D95). The agent is blocked on it.
//
// Whatever is typed here goes to the daemon and from there into the agent's
// environment: the agent is told what happened — the account it has, the
// variable a secret is in, or that you refused — and never the value, which
// is the whole reason this is a card and not a question you answer in words.
//
// group is every request the answer goes to: the project's chat answers
// several agents asking for the same thing at once. bare leaves out the line
// saying what is needed, for a card under a row that already says it.
export function CredentialCard({ question, group = [question], bare = false }: { question: T.Question; group?: readonly T.Question[]; bare?: boolean }) {
  const t = useT();
  if (question.kind === RequestConnector) return <ConnectorRequestCard question={question} group={group} bare={bare} />;
  const waiting = group.filter(isOpen);
  return (
    <div className="mt-1.5 grid min-w-0 grid-cols-[minmax(0,1fr)] gap-2" data-thread-credential={question.id}>
      {!bare && (
        <p className="flex items-center gap-1.5 text-[13px] font-medium text-secondary">
          <KeyRound className="size-3.5 shrink-0 text-subtle" />
          <span className="min-w-0 break-words">
            {question.kind === A.CredentialSecret ? (
              t.rich('chat.credential.needsSecret', { name: <code className="font-mono text-[12px]">${question.secretName}</code> })
            ) : (
              t('chat.credential.needsGitHub')
            )}
          </span>
        </p>
      )}
      <RequestReasons group={group} />
      {question.kind === A.CredentialSecret && question.secretName && <AIToolSecretWarning name={question.secretName} />}
      {question.status === 'cancelled' && (
        <p className="break-words text-[12px] text-faint" data-credential-cancelled>
          {t('chat.connector.cancelled')} {question.answer || t('chat.connector.nobodyAnswered')}
        </p>
      )}
      {waiting.length > 0 && <CredentialAnswer question={question} group={waiting} />}
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
    <div className="mb-3 max-w-xl rounded-xl border border-line bg-surface-faint px-3.5 py-2.5" data-chat-credential={question.id}>
      <WaitingLabel />
      <CredentialCard question={question} />
    </div>
  );
}

// ProjectCredentialCards are the credential requests waiting in a project,
// in its chat, where you are: one line each saying who asks and for what,
// opening to the same card as the agent's own chat, and answered through the
// same route, so an answer or a cancellation in either settles both. Agents
// asking for the same thing (any GitHub account, one secret, one connector)
// share a line, and one answer goes to all of them.
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
  return (
    <div className="grid max-w-xl grid-cols-[minmax(0,1fr)] gap-2 pb-4" data-project-credentials>
      {groupRequests(requests).map((g) => (
        <RequestRow
          key={g.key}
          group={g.questions}
          title={g.questions.length === 1 ? agents.data?.find((a) => a.project === project && a.name === g.questions[0].agent)?.title : undefined}
          onClose={() => setClosed((prev) => new Set([...prev, ...g.questions.map((q) => q.id)]))}
        />
      ))}
    </div>
  );
}

// RequestRow is one line of the project's requests: a dot while it waits, who
// asks for what, and Answer, which opens the card under it. Settled, it says
// how, with a close button in place of Answer.
function RequestRow({ group, title, onClose }: { group: T.Question[]; title?: string; onClose: () => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const q = group[0];
  const waiting = isOpen(q);
  const Icon = q.kind === RequestConnector ? Plug : KeyRound;
  return (
    <div className="min-w-0 rounded-xl border border-line bg-surface-faint" data-project-credential={q.id} data-request-group={group.map((x) => x.id).join(' ')}>
      <div className="flex min-w-0 items-center gap-2 py-1.5 pr-1.5 pl-3">
        <span aria-hidden className={cn('size-1.5 shrink-0 rounded-full', waiting ? 'bg-amber-400' : 'bg-ghost')} />
        <Icon className="size-3.5 shrink-0 text-subtle" />
        <p className="min-w-0 flex-1 truncate text-[13px] text-tertiary" data-credential-asker={group.map((x) => x.agent).join(' ')}>
          {requestSummary(t, group)}
          {title && <span className="text-faint"> · {title}</span>}
        </p>
        {waiting ? (
          <Button size="sm" variant={open ? 'ghost' : 'primary'} className="shrink-0" aria-expanded={open} onClick={() => setOpen(!open)} data-request-toggle>
            {open && <ChevronUp />}
            {open ? t('chat.request.hide') : t('chat.request.answer')}
          </Button>
        ) : (
          <>
            <span className="max-w-[45%] shrink truncate text-[12px] text-subtle" data-request-settled>
              {q.status === 'answered' ? answeredLine(q) : t('chat.credential.cancelled')}
            </span>
            <button className="shrink-0 rounded p-1 text-faint transition hover:text-primary" aria-label={t('common.close')} onClick={onClose}>
              <X className="size-3.5" />
            </button>
          </>
        )}
      </div>
      {waiting && open && (
        <div className="border-t border-line px-3 pb-3">
          <CredentialCard question={q} group={group} bare />
        </div>
      )}
    </div>
  );
}

// requestSummary is who asks for what, in one line: "agent-12 and agent-94
// need a GitHub account".
function requestSummary(t: Translate, group: readonly T.Question[]) {
  const q = group[0];
  const agents = <span className="font-mono text-[12px] text-primary">{formatList(group.map((x) => x.agent))}</span>;
  const count = group.length;
  if (q.kind === RequestConnector) {
    const req = connectorRequest(q);
    return t.rich('chat.request.connector', { agents, count, name: <span className="font-mono text-[12px] text-secondary">{req?.preset?.label ?? req?.name ?? '?'}</span> });
  }
  if (q.kind === A.CredentialSecret) return t.rich('chat.request.secret', { agents, count, name: <span className="font-mono text-[12px] text-secondary">${q.secretName}</span> });
  return t.rich('chat.request.github', { agents, count });
}

// answeredLine says how a request was settled without repeating what the
// agent was told, which is written for the agent.
function answeredLine(q: T.Question): string {
  if (q.answer?.startsWith('refused')) return t('chat.credential.refusedLine');
  if (q.kind === RequestConnector) return t('chat.credential.connectorLine');
  return q.kind === A.CredentialSecret ? t('chat.credential.secretLine', { name: `$${q.secretName}` }) : t('chat.credential.githubLine');
}

function CredentialAnswer({ question, group }: { question: T.Question; group: readonly T.Question[] }) {
  const t = useT();
  const [refusing, setRefusing] = useState(false);
  const [reason, setReason] = useState('');
  const answer = useAnswerRequests(group);
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
            aria-label={t('chat.connector.whyRefuse')}
            className="h-7 min-w-0 flex-1 text-[13px]"
            placeholder={t('chat.connector.whyPlaceholder')}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
          <Button type="submit" size="sm" variant="danger" disabled={answer.isPending}>
            {t('chat.connector.refuse')}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setRefusing(false)}>
            {t('common.cancel')}
          </Button>
        </form>
      ) : (
        <Button size="sm" variant="ghost" className="justify-self-start text-subtle" onClick={() => setRefusing(true)}>
          <Ban />
          {t('chat.connector.refuse')}
        </Button>
      )}
      {answer.error && <p className="break-words text-[12px] text-rose-300">{errorMessage(answer.error)}</p>}
    </div>
  );
}

// SecretForm is the value, under the name the agent asked for. It is stored as
// the project's Settings → Secrets stores it, so every agent of the project gets it.
function SecretForm({ question, busy, onGive }: { question: T.Question; busy: boolean; onGive: (value: string) => void }) {
  const t = useT();
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
          aria-label={t('chat.credential.secretName')}
          className="h-7 w-[42%] min-w-0 font-mono text-[12px] text-subtle"
          value={question.secretName ?? ''}
        />
        <Input
          type="password"
          autoComplete="off"
          aria-label={t('chat.credential.valueOf', { name: question.secretName ?? '' })}
          className="h-7 min-w-0 flex-1 font-mono text-[12px]"
          placeholder={t('chat.credential.value')}
          value={value}
          onChange={(event) => setValue(event.target.value)}
        />
      </div>
      <p className="text-[12px] leading-relaxed text-faint">{t('chat.credential.secretHint')}</p>
      <Button type="submit" size="sm" variant="primary" className="justify-self-end" disabled={!value || busy}>
        {busy ? <LoaderCircle className="animate-spin" /> : <KeyRound />}
        {t('chat.connector.saveAndGive')}
      </Button>
    </form>
  );
}

// GitHubForm picks one of this machine's GitHub accounts, or logs a new one in
// the way agentbox auth github does: a token, checked against GitHub and
// stored under a name. Either becomes the project's account and the agent's.
function GitHubForm({ busy, onGive }: { busy: boolean; onGive: (account: string) => void }) {
  const t = useT();
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
        <ul className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1" aria-label={t('chat.credential.githubAccounts')}>
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
                <span className="min-w-0 shrink truncate font-mono text-[12px] text-secondary">{acc.name}</span>
                {acc.login && <span className="min-w-0 truncate text-[12px] text-subtle">{acc.login}</span>}
                <span className="ml-auto shrink-0 text-[12px] text-faint">{t('chat.credential.give')}</span>
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
              aria-label={t('chat.credential.accountName')}
              className="h-7 w-[38%] min-w-0 font-mono text-[12px]"
              placeholder={t('chat.credential.namePlaceholder')}
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
            <Input
              type="password"
              autoComplete="off"
              aria-label={t('chat.credential.githubToken')}
              className="h-7 min-w-0 flex-1 font-mono text-[12px]"
              placeholder={t('chat.credential.tokenPlaceholder')}
              value={token}
              onChange={(event) => setToken(event.target.value)}
            />
          </div>
          <div className="flex items-center justify-end gap-1.5">
            <Button size="sm" variant="ghost" onClick={() => setAdding(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" size="sm" variant="primary" disabled={!name.trim() || !token.trim() || login.isPending || busy}>
              {login.isPending ? <LoaderCircle className="animate-spin" /> : <KeyRound />}
              {t('chat.credential.logInAndGive')}
            </Button>
          </div>
          {login.error && <p className="break-words text-[12px] text-rose-300">{errorMessage(login.error)}</p>}
        </form>
      ) : (
        <Button size="sm" variant="ghost" className="justify-self-start" onClick={() => setAdding(true)}>
          <Plus />
          {t('chat.credential.logInAnother')}
        </Button>
      )}
      <p className="text-[12px] leading-relaxed text-faint">{t('chat.credential.githubHint')}</p>
    </div>
  );
}
