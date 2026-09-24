import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Eye, EyeOff, KeyRound, LoaderCircle, Lock, Plus, X } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { errorMessage, timeAgo } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Card, Code, Notice } from './ui/card';
import { Field, Input } from './ui/input';
import { Tip } from './ui/tooltip';

// The Secrets tab: API keys and tokens handed to a project's agents, or to one
// agent. A target is "pawly" or "pawly/agent-01".
//
// The value field is write-only, and not because the app hides it: the daemon
// has no route that returns a value, so there is nothing here to reveal. What
// the page can show is a name, where it applies, when it was last set, and
// which agents have it.

export function SecretsTab({ target }: { target: string }) {
  const queryClient = useQueryClient();
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
            title={`From the project (${inherited.length})`}
            icon={Lock}
            description="Every agent of this project gets these. Change them on the project's Secrets tab."
          >
            <ul className="grid gap-2" aria-label="Project secrets">
              {inherited.map((secret) => (
                <SecretRow key={secret.name} secret={secret} />
              ))}
            </ul>
          </Card>
        )}

        <Card
          title={forAgent ? `This agent's own (${mine.length})` : `Project secrets (${mine.length})`}
          icon={KeyRound}
          description={
            forAgent
              ? 'Only this agent gets these. A name its project also uses is overridden here, for this agent alone.'
              : 'Every agent of this project gets these, including the ones you make later.'
          }
        >
          {secrets.error && <Notice>{errorMessage(secrets.error)}</Notice>}
          {secrets.isPending && <p className="py-3 text-sm text-subtle">Loading…</p>}
          {!secrets.isPending && mine.length === 0 && (
            <p className="py-3 text-[13px] leading-relaxed text-subtle">
              None yet. An agent reads a secret as <Code>$NAME</Code> in its shell and in its AI tool, which beats pasting a key into the chat — that would
              store it as plain text in the conversation.
            </p>
          )}
          <ul className="grid gap-2" aria-label="Secrets">
            {mine.map((secret) => (
              <SecretRow key={secret.name} secret={secret} onDelete={() => setDeleting(secret)} />
            ))}
          </ul>
        </Card>
      </div>

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={`Remove ${deleting?.name}?`}
        description={
          deleting?.scope === 'project'
            ? `AgentBox forgets the value and takes the variable out of every agent of ${deleting?.project}. A process already running in one keeps the value it read when it started.`
            : `AgentBox forgets the value and takes the variable out of ${deleting?.agent}. A process already running there keeps the value it read when it started.`
        }
        confirmLabel="Remove"
        destructive
        onConfirm={async () => {
          await api.removeSecret(target, deleting!.name);
          toast(`Removed ${deleting!.name}`);
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
  const [name, setName] = useState('');
  const [value, setValue] = useState('');
  const [visible, setVisible] = useState(false);
  const save = useMutation({
    mutationFn: () => api.setSecret(target, name.trim(), value),
    onSuccess: async (secret) => {
      setName('');
      setValue('');
      setVisible(false);
      toast(`${secret.name} stored`, {
        description: secret.agents.length > 0 ? `Written into ${secret.agents.join(', ')}` : 'The agents you make next will get it.',
      });
      await onSaved();
    },
  });
  const ready = /^[A-Z_][A-Z0-9_]*$/.test(name.trim()) && value !== '';

  return (
    <Card
      title={forAgent ? 'Give this agent a secret' : 'Give the project a secret'}
      icon={Plus}
      description={
        forAgent
          ? 'It becomes an environment variable in this agent, and in no other.'
          : 'It becomes an environment variable in every agent of this project, now and later.'
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
          <Field label="Name" htmlFor="secret-name" hint="An environment variable name, like OPENAI_API_KEY.">
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
          <Field label="Value" htmlFor="secret-value" hint="Stored encrypted and written into the agents. You can't read it back here afterwards.">
            <div className="flex items-center gap-2">
              <Input
                id="secret-value"
                type={visible ? 'text' : 'password'}
                className="font-mono text-[13px]"
                placeholder="paste the key"
                autoComplete="off"
                spellCheck={false}
                value={value}
                onChange={(event) => setValue(event.target.value)}
              />
              <Tip label={visible ? 'Hide' : 'Show while typing'}>
                <Button type="button" variant="ghost" size="icon" aria-label={visible ? 'Hide the value' : 'Show the value'} onClick={() => setVisible((v) => !v)}>
                  {visible ? <EyeOff /> : <Eye />}
                </Button>
              </Tip>
            </div>
          </Field>
        </div>
        {save.error && <Notice>{errorMessage(save.error)}</Notice>}
        <div className="flex items-center gap-3">
          <p className="text-xs leading-relaxed text-subtle">
            Agents are told which names they have and not to print, commit or echo a value.
          </p>
          <Button type="submit" variant="primary" className="ml-auto" disabled={!ready || save.isPending}>
            {save.isPending ? <LoaderCircle className="animate-spin" /> : <KeyRound />}
            Store secret
          </Button>
        </div>
      </form>
    </Card>
  );
}

// SecretRow is one secret: its name, its scope, when it was last set and where
// it is. No value, and no control that would ask for one.
function SecretRow({ secret, onDelete }: { secret: T.Secret; onDelete?: () => void }) {
  return (
    <li
      data-secret={secret.name}
      data-secret-scope={secret.scope}
      className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-xl border border-line-faint bg-surface-faint px-3 py-2.5 transition hover:border-line-strong"
    >
      <span className="font-mono text-[13px] text-primary">${secret.name}</span>
      <Badge variant={secret.scope === 'project' ? 'info' : 'brand'}>{secret.scope === 'project' ? 'project' : 'this agent'}</Badge>
      <span className="text-[11.5px] text-subtle" title={new Date(secret.updatedAt).toLocaleString()}>
        updated {timeAgo(secret.updatedAt)}
      </span>
      <span className="text-[11.5px] text-subtle">{whereItIs(secret)}</span>
      {onDelete && (
        <Tip label="Remove">
          <Button variant="ghost" size="icon-sm" className="ml-auto" aria-label={`Remove ${secret.name}`} onClick={onDelete}>
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
  if (secret.agents.length === 0) return '· in no agent yet';
  if (secret.scope === 'agent') return '· in this agent';
  if (secret.agents.length === 1) return `· in ${secret.agents[0].split('/')[1]}`;
  return `· in ${secret.agents.length} agents`;
}
