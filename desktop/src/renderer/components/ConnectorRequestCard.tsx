import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Ban, ExternalLink, LoaderCircle, Plug } from 'lucide-react';
import { useEffect, useState } from 'react';
import type * as T from '../../shared/api';
import * as A from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { type ConnectorRequest, connectorRequest, RequestConnector } from '../lib/connectors';
import { errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Input } from './ui/input';

// ConnectorRequestCard is an agent's request_connector as it stands, and what
// it takes to answer it: built like CredentialCard, and shown wherever that
// is (the agent's chat, the project's chat), plus Settings → Connectors.
//
// Answering it adds the connector to the project, the way a requested secret
// is saved as the project's, signs in when it signs in with the browser, and
// tells the agent which connector it has. The agent chooses the name and the
// URL, so the server's host is shown as plainly as the name, and a preset's
// label only when the URL is that preset's own (connectorRequest).
export function ConnectorRequestCard({ question }: { question: T.Question }) {
  const t = useT();
  const req = connectorRequest(question);
  const waiting = question.status === 'pending' || question.status === 'escalated';
  return (
    <div className="mt-1.5 grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1.5" data-thread-connector={question.id}>
      <p className="flex items-center gap-1.5 text-[12px] font-medium text-secondary">
        <Plug className="size-3.5 shrink-0 text-amber-300/90" />
        <span className="min-w-0 break-words">
          {t.rich('chat.connector.needs', {
            name: <code className="font-mono text-[11.5px]">{req?.preset?.label ?? req?.name ?? '?'}</code>,
          })}
        </span>
      </p>
      {req?.host && (
        <p className="min-w-0 text-[12px] text-secondary [overflow-wrap:anywhere]" title={req.url} data-connector-host>
          {t.rich('chat.connector.at', { host: <span className="font-mono font-semibold text-primary">{req.host}</span> })}
          {!req.preset && <span className="text-faint"> · {t('chat.connector.unknownServer')}</span>}
        </p>
      )}
      <p className="whitespace-pre-wrap text-[12px] leading-relaxed text-tertiary [overflow-wrap:anywhere]">{question.question}</p>
      {question.status === 'cancelled' && (
        <p className="break-words text-[11px] text-faint" data-connector-cancelled>
          {t('chat.connector.cancelled')} {question.answer || t('chat.connector.nobodyAnswered')}
        </p>
      )}
      {waiting && req && <ConnectorAnswer question={question} req={req} />}
    </div>
  );
}

// ConnectorRequestCards are the connector requests waiting in a project — or
// from one agent of it — at the head of Settings → Connectors.
export function ConnectorRequestCards({ project, agent }: { project: string; agent?: string }) {
  const t = useT();
  const questions = useQuery({ queryKey: ['questions', project], queryFn: () => api.questions(project) });
  const waiting = (questions.data ?? [])
    .filter((q) => q.kind === RequestConnector && (q.status === 'pending' || q.status === 'escalated') && (!agent || q.agent === agent))
    .toSorted((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
  if (waiting.length === 0) return null;
  return (
    <div className="grid gap-3" data-connector-requests>
      {waiting.map((q) => (
        <div key={q.id} className="rounded-2xl border border-amber-400/25 bg-amber-400/[0.04] px-4 py-3" data-connector-request={q.id}>
          <p className="text-[10px] font-semibold uppercase tracking-[0.07em] text-amber-300/90">
            {t.rich('chat.connector.waitingOnYou', { agent: <span className="font-mono normal-case tracking-normal text-secondary">{q.agent}</span> })}
          </p>
          <ConnectorRequestCard question={q} />
        </div>
      ))}
    </div>
  );
}

function ConnectorAnswer({ question, req }: { question: T.Question; req: ConnectorRequest }) {
  const t = useT();
  const project = question.project;
  const queryClient = useQueryClient();
  const connectors = useQuery({ queryKey: ['connectors', project], queryFn: () => api.connectors(project) });
  const existing = connectors.data?.find((c) => c.name === req.name);
  const [refusing, setRefusing] = useState(false);
  const [reason, setReason] = useState('');
  const [token, setToken] = useState('');
  const [signingIn, setSigningIn] = useState<string | undefined>();

  const answer = useMutation({
    mutationFn: (body: Pick<T.AnswerCredentialRequest, 'connector' | 'refuse' | 'reason'>) => api.answerCredential(project, question.id, body),
    onSuccess: (answered) =>
      queryClient.setQueryData<T.Question[]>(['questions', project], (list) => list?.map((q) => (q.id === answered.id ? answered : q))),
  });

  // Adding it, and signing in when it signs in with the browser. A token
  // server is answered as soon as it is added: there is nothing to wait for.
  const add = useMutation({
    mutationFn: async () => {
      const secret = req.preset?.secret;
      if (!existing) {
        if (secret) await api.setSecret(project, secret.name, token);
        await api.setConnector(project, req.name, {
          url: req.url,
          ...(secret ? { auth: A.ConnectorSecret, secret: secret.name, header: secret.header, scheme: secret.scheme } : {}),
        });
      }
      if (secret || existing?.auth === A.ConnectorSecret || existing?.auth === A.ConnectorNone) return undefined;
      const result = await api.connectConnector(project, req.name);
      await window.agentbox.openExternal(result.authorizationUrl);
      return result.authorizationUrl;
    },
    onSuccess: async (url) => {
      setToken('');
      await queryClient.invalidateQueries({ queryKey: ['connectors'] });
      if (url) setSigningIn(url);
      else answer.mutate({ connector: req.name });
    },
  });

  // The sign-in finishing is what answers the agent.
  const connected = existing?.status === A.ConnectorConnected;
  useEffect(() => {
    if (signingIn && connected && !answer.isPending && !answer.isSuccess) {
      setSigningIn(undefined);
      answer.mutate({ connector: req.name });
    }
  }, [signingIn, connected, answer, req.name]);

  const busy = add.isPending || answer.isPending;
  const needsToken = !existing && !!req.preset?.secret;
  const browserWait = signingIn && existing?.status === A.ConnectorConnecting;

  return (
    <div className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1.5">
      {connected ? (
        <Button size="sm" variant="primary" className="justify-self-end" disabled={busy} onClick={() => answer.mutate({ connector: req.name })}>
          {busy ? <LoaderCircle className="animate-spin" /> : <Plug />}
          {t('chat.connector.give', { name: req.name })}
        </Button>
      ) : browserWait ? (
        <div className="flex min-w-0 items-center gap-1.5 text-[11.5px] text-subtle">
          <LoaderCircle className="size-3.5 animate-spin" />
          {t('chat.connector.waitingSignIn')}
          <Button size="sm" variant="ghost" className="ml-auto" onClick={() => void window.agentbox.openExternal(signingIn)}>
            <ExternalLink />
            {t('chat.connector.openAgain')}
          </Button>
        </div>
      ) : !req.url && !existing ? (
        <p className="text-[11px] leading-relaxed text-faint">{t('chat.connector.noUrl')}</p>
      ) : (
        <form
          className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1.5"
          onSubmit={(event) => {
            event.preventDefault();
            if (!busy && (!needsToken || token)) add.mutate();
          }}
        >
          {needsToken && (
            <>
              <Input
                type="password"
                autoComplete="off"
                aria-label={req.preset!.secret!.tokenLabel}
                className="h-7 min-w-0 font-mono text-[11.5px]"
                placeholder={req.preset!.secret!.tokenLabel}
                value={token}
                onChange={(event) => setToken(event.target.value)}
              />
              <p className="text-[10.5px] leading-relaxed text-faint">{req.preset!.secret!.why}</p>
            </>
          )}
          {!needsToken && (
            <p className="text-[10.5px] leading-relaxed text-faint">
              {t('chat.connector.addedHint')}
            </p>
          )}
          <Button type="submit" size="sm" variant="primary" className="justify-self-end" disabled={busy || (needsToken && !token)}>
            {busy ? <LoaderCircle className="animate-spin" /> : <Plug />}
            {needsToken ? t('chat.connector.saveAndGive') : existing ? t('chat.connector.connect') : t('chat.connector.addAndConnect')}
          </Button>
        </form>
      )}

      {refusing ? (
        <form
          className="flex min-w-0 items-center gap-1.5"
          onSubmit={(event) => {
            event.preventDefault();
            if (!answer.isPending) answer.mutate({ refuse: true, reason: reason.trim() || undefined });
          }}
        >
          <Input
            autoFocus
            aria-label={t('chat.connector.whyRefuse')}
            className="h-7 min-w-0 flex-1 text-[12px]"
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
      {(add.error || answer.error) && <p className="break-words text-[11px] text-rose-300">{errorMessage(add.error ?? answer.error)}</p>}
      {existing?.status === A.ConnectorError && existing.error && <p className="break-words text-[11px] text-rose-300">{existing.error}</p>}
    </div>
  );
}
