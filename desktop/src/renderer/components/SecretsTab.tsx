import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Eye, EyeOff, KeyRound, LoaderCircle, Lock, Plus, X } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatDateTime, t as tt, useT } from '../lib/i18n';
import { projectLabel } from '../lib/projectName';
import { errorMessage, timeAgo } from '../lib/utils';
import { BrowserCookiesCard } from './BrowserCookies';
import { ConfirmDialog } from './ConfirmDialog';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Card, Code, Notice } from './ui/card';
import { Field, Input } from './ui/input';
import { Tip } from './ui/tooltip';

// Secrets, a section of a project's or an agent's Settings tab: API keys and tokens handed to a project's agents, or to one
// agent. A target is "pawly" or "pawly/agent-01".
//
// The value field is write-only, and not because the app hides it: the daemon
// has no route that returns a value, so there is nothing here to reveal. What
// the page can show is a name, where it applies, when it was last set, and
// which agents have it.

export function SecretsTab({ target }: { target: string }) {
  const t = useT();
  const queryClient = useQueryClient();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const forAgent = target.includes('/');
  const secrets = useQuery({ queryKey: ['secrets', target], queryFn: () => api.secrets(target) });
  const [deleting, setDeleting] = useState<T.Secret | null>(null);

  // An agent's list carries its project's secrets too, marked with their
  // scope. They are shown above its own, read-only: they belong to the project.
  const mine = secrets.data?.filter((s) => (forAgent ? s.scope === 'agent' : s.scope === 'project')) ?? [];
  const inherited = forAgent ? (secrets.data?.filter((s) => s.scope === 'project') ?? []) : [];
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['secrets'] });

  return (
    <div className="h-full overflow-y-auto p-5">
      <div className="mx-auto grid max-w-4xl gap-4">
        <SecretForm target={target} forAgent={forAgent} onSaved={refresh} />

        {inherited.length > 0 && (
          <Card
            title={t('agent.secrets.fromProject', { count: inherited.length })}
            icon={Lock}
            description={t('agent.secrets.fromProjectDescription')}
          >
            <ul className="grid gap-2" aria-label={t('agent.secrets.projectList')}>
              {inherited.map((secret) => (
                <SecretRow key={secret.name} secret={secret} />
              ))}
            </ul>
          </Card>
        )}

        <Card
          title={forAgent ? t('agent.secrets.ownTitle', { count: mine.length }) : t('agent.secrets.projectTitle', { count: mine.length })}
          icon={KeyRound}
          description={
            forAgent
              ? t('agent.secrets.ownDescription')
              : t('agent.secrets.projectDescription')
          }
        >
          {secrets.error && <Notice>{errorMessage(secrets.error)}</Notice>}
          {secrets.isPending && <p className="py-3 text-sm text-subtle">{t('common.loading')}</p>}
          {!secrets.isPending && mine.length === 0 && (
            <p className="py-3 text-[13px] leading-relaxed text-subtle">
              {t.rich('agent.secrets.none', { code: (c) => <Code>{c}</Code> })}
            </p>
          )}
          <ul className="grid gap-2" aria-label={t('agent.secrets.list')}>
            {mine.map((secret) => (
              <SecretRow key={secret.name} secret={secret} onDelete={() => setDeleting(secret)} />
            ))}
          </ul>
        </Card>

        {!forAgent && <BrowserCookiesCard project={target} />}
      </div>

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('agent.secrets.removeTitle', { name: deleting?.name ?? '' })}
        description={
          deleting?.scope === 'project'
            ? t('agent.secrets.removeProject', { project: projectLabel(deleting?.project ?? '', projects.data) })
            : t('agent.secrets.removeAgent', { agent: deleting?.agent ?? '' })
        }
        confirmLabel={t('common.remove')}
        destructive
        onConfirm={async () => {
          await api.removeSecret(target, deleting!.name);
          toast(t('agent.secrets.removed', { name: deleting!.name }));
          await refresh();
        }}
      />
    </div>
  );
}

// SecretForm stores a secret. The value box is masked, can be shown while you
// type it, and is cleared as soon as it is stored: there is nothing to come
// back to, since no page can read a stored value.
function SecretForm({ target, forAgent, onSaved }: { target: string; forAgent: boolean; onSaved: () => Promise<unknown> }) {
  const t = useT();
  const [name, setName] = useState('');
  const [value, setValue] = useState('');
  const [visible, setVisible] = useState(false);
  const save = useMutation({
    mutationFn: () => api.setSecret(target, name.trim(), value),
    onSuccess: async (secret) => {
      setName('');
      setValue('');
      setVisible(false);
      toast(t('agent.secrets.stored', { name: secret.name }), {
        description: secret.agents.length > 0 ? t('agent.secrets.writtenInto', { agents: secret.agents.join(', ') }) : t('agent.secrets.nextAgents'),
      });
      await onSaved();
    },
  });
  const ready = /^[A-Z_][A-Z0-9_]*$/.test(name.trim()) && value !== '';

  return (
    <Card
      title={forAgent ? t('agent.secrets.giveAgent') : t('agent.secrets.giveProject')}
      icon={Plus}
      description={
        forAgent
          ? t('agent.secrets.giveAgentDescription')
          : t('agent.secrets.giveProjectDescription')
      }
    >
      <form
        className="grid gap-4"
        onSubmit={(event) => {
          event.preventDefault();
          if (ready) save.mutate();
        }}
      >
        <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
          <Field label={t('agent.secrets.name')} htmlFor="secret-name" hint={t('agent.secrets.nameHint')}>
            <Input
              id="secret-name"
              className="font-mono text-[13px]"
              placeholder="OPENAI_API_KEY"
              autoComplete="off"
              spellCheck={false}
              value={name}
              onChange={(event) => setName(event.target.value.toUpperCase())}
            />
          </Field>
          <Field label={t('agent.secrets.value')} htmlFor="secret-value" hint={t('agent.secrets.valueHint')}>
            <div className="flex items-center gap-2">
              <Input
                id="secret-value"
                type={visible ? 'text' : 'password'}
                className="font-mono text-[13px]"
                placeholder={t('agent.secrets.valuePlaceholder')}
                autoComplete="off"
                spellCheck={false}
                value={value}
                onChange={(event) => setValue(event.target.value)}
              />
              <Tip label={visible ? t('agent.secrets.hide') : t('agent.secrets.showWhileTyping')}>
                <Button type="button" variant="ghost" size="icon" aria-label={visible ? t('agent.secrets.hideValue') : t('agent.secrets.showValue')} onClick={() => setVisible((v) => !v)}>
                  {visible ? <EyeOff /> : <Eye />}
                </Button>
              </Tip>
            </div>
          </Field>
        </div>
        {save.error && <Notice>{errorMessage(save.error)}</Notice>}
        <div className="flex items-center gap-3">
          <p className="text-xs leading-relaxed text-subtle">
            {t('agent.secrets.note')}
          </p>
          <Button type="submit" variant="primary" className="ml-auto" disabled={!ready || save.isPending}>
            {save.isPending ? <LoaderCircle className="animate-spin" /> : <KeyRound />}
            {t('agent.secrets.store')}
          </Button>
        </div>
      </form>
    </Card>
  );
}

// SecretRow is one secret: its name, its scope, when it was last set and where
// it is. No value, and no control that would ask for one.
function SecretRow({ secret, onDelete }: { secret: T.Secret; onDelete?: () => void }) {
  const t = useT();
  return (
    <li
      data-secret={secret.name}
      data-secret-scope={secret.scope}
      className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-xl border border-line-faint bg-surface-faint px-3 py-2.5 transition hover:border-line-strong"
    >
      <span className="font-mono text-[13px] text-primary">${secret.name}</span>
      <Badge variant={secret.scope === 'project' ? 'info' : 'brand'}>{secret.scope === 'project' ? t('agent.secrets.scopeProject') : t('agent.secrets.scopeAgent')}</Badge>
      <span className="text-[11.5px] text-subtle" title={formatDateTime(secret.updatedAt)}>
        {t('agent.secrets.updated', { when: timeAgo(secret.updatedAt) })}
      </span>
      <span className="text-[11.5px] text-subtle">{whereItIs(secret)}</span>
      {onDelete && (
        <Tip label={t('common.remove')}>
          <Button variant="ghost" size="icon-sm" className="ml-auto" aria-label={t('agent.secrets.removeLabel', { name: secret.name })} onClick={onDelete}>
            <X />
          </Button>
        </Tip>
      )}
    </li>
  );
}

// whereItIs says which agents hold the secret now. A project secret in several
// agents is counted: the answer that matters is "all of them", and the list
// would push the name off the row.
function whereItIs(secret: T.Secret): string {
  if (secret.agents.length === 0) return tt('agent.secrets.inNone');
  if (secret.scope === 'agent') return tt('agent.secrets.inThis');
  if (secret.agents.length === 1) return tt('agent.secrets.inOne', { agent: secret.agents[0].split('/')[1] });
  return tt('agent.secrets.inMany', { count: secret.agents.length });
}
