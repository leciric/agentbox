import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowDown, ArrowRight, GitBranch, LoaderCircle, Pencil, Plus, Stethoscope, Trash2 } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { t, useT } from '../lib/i18n';
import type { SettingGroup } from '../lib/settingsSearch';
import { cn, errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Field, Input, Label } from './ui/input';
import { Select, SelectOption } from './ui/select';
import { SettingNote } from './ui/settings';

// A project's git profiles: where its cluster agents clone and push, and how
// each one signs in. There are two, private (the server work in progress stays
// on) and public (where a finished change is published), and the project picks
// which of them its agents use (internal/daemon/gitprofiles.go,
// internal/agent/gitprofile.go).
//
// A login is written once and never read back, as with secrets: the daemon
// answers the credential's name and whether it is stored, and there is no call
// that returns the value. Saving a profile and its login is one request, so a
// profile never names a login that isn't there, and Test asks the remote for
// its refs with that login before any pod is made. The create dialog reads
// the same query and refuses a project whose profile a pod couldn't clone
// with, so what these cards say is what creating an agent checks.

type GitProfile = 'private' | 'public';

// projectGitKey is the query the cards and the create dialog share, so a save
// here is what the dialog checks a moment later.
export const projectGitKey = (project: string) => ['projectGit', project];

export function gitProfileGroups(project: T.Project): SettingGroup[] {
  return [
    {
      id: 'git',
      title: t('settings.git.title'),
      description: t('settings.git.description'),
      entries: [
        {
          id: 'git-flow',
          label: t('settings.git.flowLabel'),
          keywords: t('settings.git.flowKeywords'),
          render: () => <GitFlow project={project.name} />,
        },
        {
          id: 'git-private',
          label: t('settings.git.privateLabel'),
          keywords: t('settings.git.privateKeywords'),
          render: () => <GitProfileCard project={project.name} profile="private" />,
        },
        {
          id: 'git-public',
          label: t('settings.git.publicLabel'),
          keywords: t('settings.git.publicKeywords'),
          render: () => <GitProfileCard project={project.name} profile="public" />,
        },
      ],
    },
  ];
}

function useProjectGit(project: string) {
  return useQuery({ queryKey: projectGitKey(project), queryFn: () => api.projectGit(project) });
}

// GitFlow draws how an agent's branch moves through the two remotes: pushed to
// the private one while it is work in progress, published to the public one
// when it is finished, with the step agents push to marked. The words carry
// what the colours show (the step agents push to says so, and a skipped step
// says it is skipped), and it draws nothing until the profiles are read: the
// cards below say loading and errors. It stacks by the settings column's
// width, which a wide window can still leave narrow.
function GitFlow({ project }: { project: string }) {
  const t = useT();
  const git = useProjectGit(project);
  if (!git.data) return null;
  const agentRemote = git.data.agentRemote || 'private';
  const has = (profile: GitProfile) => (git.data.profiles ?? []).some((p) => p.profile === profile);
  const steps: { id: string; title: string; hint: string; used?: boolean; skipped?: boolean }[] = [
    { id: 'agent', title: t('settings.git.flowAgent'), hint: t('settings.git.flowAgentHint') },
    {
      id: 'private',
      title: gitProfileName('private'),
      hint: agentRemote === 'public' ? t('settings.git.flowPrivateSkipped') : has('private') ? t('settings.git.flowPrivateHint') : t('settings.git.flowNotSetUp'),
      used: agentRemote === 'private',
      skipped: agentRemote === 'public',
    },
    {
      id: 'public',
      title: gitProfileName('public'),
      hint: !has('public') ? t('settings.git.flowNotSetUp') : agentRemote === 'public' ? t('settings.git.flowPublicDirect') : t('settings.git.flowPublicHint'),
      used: agentRemote === 'public',
    },
  ];
  return (
    <div className="@container grid gap-3 px-5 py-4" data-git-flow={agentRemote}>
      <p className="text-[12px] leading-relaxed text-subtle">{t('settings.git.flowHint')}</p>
      <ol className="flex flex-col gap-1.5 @lg:flex-row @lg:items-stretch" aria-label={t('settings.git.flowLabel')}>
        {steps.map((step, i) => (
          <li
            key={step.id}
            aria-current={step.used ? 'step' : undefined}
            data-git-flow-step={step.id}
            className="flex min-w-0 flex-col items-stretch gap-1.5 @lg:flex-1 @lg:flex-row @lg:items-center"
          >
            {i > 0 && (
              <>
                <ArrowDown aria-hidden className="size-3.5 shrink-0 self-center text-subtle @lg:hidden" />
                <ArrowRight aria-hidden className="hidden size-3.5 shrink-0 text-subtle @lg:block" />
              </>
            )}
            <div
              className={cn(
                'grid min-w-0 flex-1 gap-0.5 self-stretch rounded-lg border border-line bg-surface-faint px-3 py-2',
                step.used && 'border-brand-500 bg-brand-500/10',
                step.skipped && 'border-dashed bg-transparent',
              )}
            >
              <span className="flex flex-wrap items-center gap-1.5">
                <span className={cn('text-[12.5px] font-medium text-primary', step.skipped && 'text-muted')}>{step.title}</span>
                {step.used && <Badge>{t('settings.git.inUse')}</Badge>}
              </span>
              <span className="text-[11.5px] leading-snug text-subtle">{step.hint}</span>
            </div>
          </li>
        ))}
      </ol>
    </div>
  );
}

// gitProfileName is the card's own name for a profile: the heading it shows.
function gitProfileName(profile: string): string {
  return profile === 'public' ? t('settings.git.publicTitle') : t('settings.git.privateTitle');
}

// gitProfileWord is the same profile as a sentence says it, lowercased the
// way the language writes it: "the private profile", "o perfil privado".
export function gitProfileWord(profile: string): string {
  return profile === 'public' ? t('settings.git.publicWord') : t('settings.git.privateWord');
}

// authName is how a profile signs in, in the app's words.
function authName(auth: string): string {
  switch (auth) {
    case 'ssh-key':
      return t('settings.git.authSSH');
    case 'github-account':
      return t('settings.git.authGitHub');
    case 'token':
      return t('settings.git.authToken');
  }
  return auth;
}

function GitProfileCard({ project, profile }: { project: string; profile: GitProfile }) {
  const t = useT();
  const git = useProjectGit(project);
  if (git.isPending) {
    return (
      <div className="px-5 py-4">
        <SettingNote>{t('common.loading')}</SettingNote>
      </div>
    );
  }
  if (git.error) {
    return (
      <div className="px-5 py-4">
        <Notice>{errorMessage(git.error)}</Notice>
      </div>
    );
  }
  const found = (git.data.profiles ?? []).find((p) => p.profile === profile);
  return <GitProfileBody project={project} profile={profile} git={git.data} found={found} />;
}

function GitProfileBody({
  project,
  profile,
  git,
  found,
}: {
  project: string;
  profile: GitProfile;
  git: T.ProjectGit;
  found?: T.ProjectGitProfile;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const name = gitProfileName(profile);
  const word = gitProfileWord(profile);
  const active = git.agentRemote === profile;
  const [editing, setEditing] = useState(false);
  const [removing, setRemoving] = useState(false);

  const choose = useMutation({
    mutationFn: () => api.selectProjectGitProfile(project, profile),
    onSuccess: (next) => {
      queryClient.setQueryData(projectGitKey(project), next);
      toast(t('settings.git.nowUsed', { profile: word }));
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  // A removal that fails says why in the confirmation, which stays open.
  const remove = useMutation({
    mutationFn: () => api.removeProjectGitProfile(project, profile),
    onSuccess: (next) => {
      queryClient.setQueryData(projectGitKey(project), next);
      toast(t('settings.git.removed', { profile: word }));
    },
  });
  const test = useMutation({
    mutationFn: () => api.testProjectGitProfile(project, profile),
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <div className="grid gap-3 px-5 py-4" data-git-profile={profile}>
      <div className="flex flex-wrap items-center gap-2">
        <GitBranch className="size-4 shrink-0 text-subtle" />
        <span className="text-[13px] font-medium text-primary">{name}</span>
        {active && <Badge variant="brand">{t('settings.git.inUse')}</Badge>}
        {found &&
          (found.hasCredential ? <Badge variant="success">{t('settings.git.loginStored')}</Badge> : <Badge variant="warning">{t('settings.git.noLogin')}</Badge>)}
        {found && (
          <span className="ml-auto flex flex-wrap items-center gap-1">
            <Button variant="ghost" size="sm" data-git-test={profile} disabled={test.isPending} onClick={() => test.mutate()}>
              {test.isPending ? <LoaderCircle className="animate-spin" /> : <Stethoscope />}
              {t('settings.git.test')}
            </Button>
            <Button variant="ghost" size="sm" data-git-edit={profile} onClick={() => setEditing(true)}>
              <Pencil />
              {t('settings.git.edit')}
            </Button>
            <Button variant="ghost" size="sm" data-git-remove={profile} onClick={() => setRemoving(true)}>
              <Trash2 />
              {t('common.remove')}
            </Button>
          </span>
        )}
      </div>

      <p className="text-[12px] leading-relaxed text-subtle">{profile === 'private' ? t('settings.git.privateHint') : t('settings.git.publicHint')}</p>
      {profile === 'public' && <SettingNote tone="warning">{t('settings.git.publicWarning')}</SettingNote>}

      {!found ? (
        <div className="grid justify-items-start gap-2">
          <SettingNote>{t('settings.git.missing')}</SettingNote>
          <Button variant="secondary" size="sm" data-git-set-up={profile} onClick={() => setEditing(true)}>
            <Plus />
            {t('settings.git.setUp')}
          </Button>
        </div>
      ) : (
        <>
          <dl className="grid gap-1.5 text-[12px] leading-relaxed">
            <GitLine label={t('settings.git.url')}>
              <span className="break-all font-mono text-tertiary">{found.url}</span>
            </GitLine>
            <GitLine label={t('settings.git.auth')}>{authName(found.auth)}</GitLine>
            <GitLine label={found.auth === 'github-account' ? t('settings.git.githubAccount') : t('settings.git.credential')}>
              <span className="break-all font-mono text-tertiary">{found.credential || found.profile}</span>
              <span className="text-subtle"> · {found.hasCredential ? t('settings.git.stored') : t('settings.git.notStored')}</span>
            </GitLine>
            <GitLine label={t('settings.git.resolved')}>
              {found.resolvedURL ? (
                <span className="break-all font-mono text-tertiary">{found.resolvedURL}</span>
              ) : (
                <span className="text-amber-300">{t('settings.git.unresolved')}</span>
              )}
            </GitLine>
          </dl>
          {found.resolveError && <Notice tone="warning">{found.resolveError}</Notice>}
          {test.data && (
            <SettingNote tone={test.data.ok ? 'muted' : 'error'}>
              {test.data.ok ? t('settings.git.testOK', { count: test.data.refs }) : t('settings.git.testFailed', { detail: test.data.detail || t('settings.git.testNoDetail') })}
            </SettingNote>
          )}
          <div className="flex flex-wrap items-center gap-3">
            <label className="flex w-fit items-center gap-2 text-[12.5px] text-muted">
              <input
                type="radio"
                name={`git-use-${project}`}
                checked={active}
                disabled={choose.isPending}
                onChange={() => !active && choose.mutate()}
                data-git-use={profile}
                className="size-3.5 accent-brand-500"
              />
              {t('settings.git.useForAgents')}
            </label>
            {choose.error && <SettingNote tone="error">{errorMessage(choose.error)}</SettingNote>}
          </div>
        </>
      )}

      {editing && (
        <GitProfileForm
          project={project}
          profile={profile}
          found={found}
          onSaved={(next) => {
            queryClient.setQueryData(projectGitKey(project), next);
            setEditing(false);
            test.reset();
            toast(t('settings.git.saved', { profile: word }));
          }}
          onCancel={() => setEditing(false)}
        />
      )}

      <ConfirmDialog
        open={removing}
        onOpenChange={setRemoving}
        title={t('settings.git.removeTitle', { profile: word })}
        description={t('settings.git.removeDescription')}
        confirmLabel={t('settings.git.removeConfirm')}
        destructive
        onConfirm={() => remove.mutateAsync()}
      />
    </div>
  );
}

function GitLine({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
      <dt className="w-28 shrink-0 text-subtle">{label}</dt>
      <dd className="min-w-0 flex-1 break-words text-secondary">{children}</dd>
    </div>
  );
}

function GitProfileForm({
  project,
  profile,
  found,
  onSaved,
  onCancel,
}: {
  project: string;
  profile: GitProfile;
  found?: T.ProjectGitProfile;
  onSaved: (git: T.ProjectGit) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const [url, setURL] = useState(found?.url ?? '');
  const [auth, setAuth] = useState(found?.auth || 'token');
  const [credential, setCredential] = useState(found?.credential ?? '');
  const [secret, setSecret] = useState('');
  const save = useMutation({
    mutationFn: () => {
      const req: T.SetProjectGitProfileRequest = { url: url.trim(), auth, credential: credential.trim() || undefined };
      // The secret is sent only when one was typed: leaving the field empty
      // keeps the stored login, which the daemon never answers back.
      if (secret) req.secret = secret;
      return api.setProjectGitProfile(project, profile, req);
    },
    onSuccess: (next) => {
      setSecret('');
      onSaved(next);
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const github = auth === 'github-account';
  const ready = url.trim() !== '' && (!github || credential.trim() !== '');
  const id = `git-${profile}`;
  // The login's name belongs to the kind of profile it was saved with: a
  // token's name is no GitHub account, and the other way round.
  const changeAuth = (next: string) => {
    setAuth(next);
    setCredential(next === found?.auth ? (found?.credential ?? '') : '');
  };

  return (
    <form
      className="grid gap-2.5 rounded-xl border border-line bg-surface-faint p-3"
      data-git-form={profile}
      onSubmit={(event) => {
        event.preventDefault();
        if (ready) save.mutate();
      }}
    >
      <p className="text-[13px] font-medium text-primary">{t('settings.git.editTitle', { profile: gitProfileWord(profile) })}</p>
      <Field label={t('settings.git.urlLabel')} htmlFor={`${id}-url`} hint={t('settings.git.urlHint')}>
        <Input
          id={`${id}-url`}
          value={url}
          onChange={(event) => setURL(event.target.value)}
          spellCheck={false}
          autoComplete="off"
          className="font-mono text-[12.5px]"
          placeholder="https://hub.example.com/h4ad/agentbox.git"
        />
      </Field>
      <div className="flex flex-wrap gap-2">
        <Field label={t('settings.git.authLabel')} htmlFor={`${id}-auth`}>
          <div className="w-44">
            <Select id={`${id}-auth`} value={auth} onChange={changeAuth}>
              <SelectOption value="token">{t('settings.git.authToken')}</SelectOption>
              <SelectOption value="ssh-key">{t('settings.git.authSSH')}</SelectOption>
              <SelectOption value="github-account">{t('settings.git.authGitHub')}</SelectOption>
            </Select>
          </div>
        </Field>
        <div className="grid min-w-44 flex-1 gap-1.5">
          <Label htmlFor={`${id}-credential`}>{github ? t('settings.git.githubAccountLabel') : t('settings.git.credentialLabel')}</Label>
          <Input
            id={`${id}-credential`}
            value={credential}
            onChange={(event) => setCredential(event.target.value)}
            spellCheck={false}
            autoComplete="off"
            className="font-mono text-[12.5px]"
            placeholder={profile}
          />
          <p className="text-xs text-subtle">{github ? t('settings.git.githubAccountHint') : t('settings.git.credentialHint')}</p>
        </div>
      </div>
      {github ? (
        <SettingNote>{t('settings.git.githubSecretNote')}</SettingNote>
      ) : (
        <Field label={t('settings.git.secretLabel')} htmlFor={`${id}-secret`} hint={t('settings.git.secretHint')}>
          <Input
            id={`${id}-secret`}
            type="password"
            value={secret}
            onChange={(event) => setSecret(event.target.value)}
            spellCheck={false}
            autoComplete="off"
            className="font-mono text-[12.5px]"
          />
        </Field>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button type="submit" variant="primary" size="sm" disabled={!ready || save.isPending}>
          {save.isPending && <LoaderCircle className="animate-spin" />}
          {t('common.save')}
        </Button>
        <Button variant="ghost" size="sm" disabled={save.isPending} onClick={onCancel}>
          {t('common.cancel')}
        </Button>
      </div>
      {save.error && <SettingNote tone="error">{errorMessage(save.error)}</SettingNote>}
    </form>
  );
}
