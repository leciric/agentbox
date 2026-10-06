import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { FolderGit2, FolderOpen, FolderPlus, LoaderCircle } from 'lucide-react';
import { useEffect, useState } from 'react';
import * as T from '../../shared/api';
import { api, ApiError } from '../lib/api';
import { t as translate, useT } from '../lib/i18n';
import { joinPath, parentOf } from '../lib/paths';
import { errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Code, Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input } from './ui/input';
import { Select, SelectOption } from './ui/select';
import { Tabs, TabsList, TabsTrigger } from './ui/tabs';

// A project is a repository that's already there, or one AgentBox starts:
// a new folder, git init on main and an initial commit, so agents have
// something to branch from (AddProjectRequest.create).
type Mode = 'existing' | 'new';

export function AddProjectDialog({
  open,
  onOpenChange,
  onAdded,
  onOpenSetup,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onAdded: (project: T.Project) => void;
  onOpenSetup: () => void;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const target = useQuery({ queryKey: ['target'], queryFn: () => window.agentbox.target.get(), staleTime: Infinity });
  // On a hub's environment, the repository is on that machine: this one's folder picker can't reach it.
  const remote = target.data?.kind === 'hub' ? target.data.environmentName : undefined;
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth, enabled: open });
  const claudeAccounts = auth.data?.claudeAccounts ?? [];
  const githubAccounts = auth.data?.githubAccounts ?? [];
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects, enabled: open });
  const [mode, setMode] = useState<Mode>('existing');
  const [path, setPath] = useState('');
  const [name, setName] = useState('');
  // A new repository is a parent folder and a name; the name is the new
  // folder's, and the project's too.
  const [parent, setParent] = useState('');
  const [folderName, setFolderName] = useState('');
  const creating = mode === 'new';
  const newPath = joinPath(parent.trim(), folderName.trim());
  // "" is this machine's default account, which is what a project gets until
  // it picks one; the accounts only show up when there is a choice to make.
  const [claudeAccount, setClaudeAccount] = useState('');
  const [githubAccount, setGitHubAccount] = useState('');
  // On Windows, the picker gives a folder on a Windows drive as /mnt/<drive>/...:
  // the daemon adds a copy of it in the WSL distro's ~/src rather than the
  // folder itself, which git would be slow in there (D91).
  const onWindowsDrive = !creating && !remote && windowsDrive.test(path.trim());
  const add = useMutation({
    // commitFiles is the user's answer to the daemon's saying the new
    // repository's folder already has files in it.
    mutationFn: (commitFiles: boolean) =>
      api.addProject({
        path: creating ? newPath : path.trim(),
        name: creating ? undefined : name.trim() || undefined,
        claudeAccount: claudeAccount || undefined,
        githubAccount: githubAccount || undefined,
        copyToLinux: onWindowsDrive || undefined,
        create: creating || undefined,
        commitFiles: (creating && commitFiles) || undefined,
      }),
    onSuccess: async (project) => {
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
      onOpenChange(false);
      onAdded(project);
    },
  });

  useEffect(() => {
    if (!open) return;
    setMode('existing');
    setPath('');
    setName('');
    setParent('');
    setFolderName('');
    setClaudeAccount('');
    setGitHubAccount('');
    add.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // The parent starts as the folder the newest project is in, which is
  // likely where the user keeps their code.
  useEffect(() => {
    if (!creating || parent) return;
    const newest = [...(projects.data ?? [])].sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
    if (newest) setParent(parentOf(newest.root));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [creating, projects.data]);

  // Another folder is another question: files in the last one say nothing
  // about this one.
  useEffect(() => {
    if (add.error) add.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [newPath]);

  // The folder has files in it: submitting again commits them.
  const notEmpty = creating && add.error instanceof ApiError && add.error.code === T.ErrorFolderNotEmpty;

  const browse = async (set: (dir: string) => void) => {
    const dir = await window.agentbox.pickDirectory();
    if (dir) set(dir);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('project.add.title')}</DialogTitle>
          <DialogDescription>
            {t.rich('project.add.description', {
              kind: creating ? 'new' : 'existing',
              where: remote ? <span className="text-secondary">{remote}</span> : t('project.add.thisMachine'),
              code: (c) => <Code>{c}</Code>,
              branch: 'agentbox/<agent>',
            })}
          </DialogDescription>
        </DialogHeader>
        <Tabs
          value={mode}
          onValueChange={(value) => {
            setMode(value as Mode);
            add.reset();
          }}
        >
          <TabsList aria-label={t('project.add.modeAria')} className="w-full">
            <TabsTrigger value="existing" className="flex-1">
              <FolderGit2 />
              {t('project.add.existing')}
            </TabsTrigger>
            <TabsTrigger value="new" className="flex-1">
              <FolderPlus />
              {t('project.add.new')}
            </TabsTrigger>
          </TabsList>
        </Tabs>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            add.mutate(notEmpty);
          }}
        >
          {creating ? (
            <>
              <Field label={t('project.add.parentLabel')} htmlFor="project-parent">
                <div className="flex gap-2">
                  <Input
                    id="project-parent"
                    className="font-mono text-[13px]"
                    placeholder="/home/you/code"
                    value={parent}
                    onChange={(event) => setParent(event.target.value)}
                  />
                  {!remote && (
                    <Button onClick={() => void browse(setParent)}>
                      <FolderOpen />
                      {t('project.add.browse')}
                    </Button>
                  )}
                </div>
              </Field>
              <Field
                label={t('project.add.nameLabel')}
                htmlFor="project-folder-name"
                hint={
                  newPath ? (
                    t.rich('project.add.newHint', { code: (c) => <Code>{c}</Code>, path: newPath })
                  ) : (
                    t('project.add.newHintEmpty')
                  )
                }
              >
                <Input id="project-folder-name" autoFocus placeholder="my-app" value={folderName} onChange={(event) => setFolderName(event.target.value)} />
              </Field>
            </>
          ) : (
            <Field label={t('project.add.folderLabel')} htmlFor="project-path">
              <div className="flex gap-2">
                <Input
                  id="project-path"
                  autoFocus
                  className="font-mono text-[13px]"
                  placeholder="/home/you/code/my-app"
                  value={path}
                  onChange={(event) => setPath(event.target.value)}
                />
                {!remote && (
                  <Button onClick={() => void browse(setPath)}>
                    <FolderOpen />
                    {t('project.add.browse')}
                  </Button>
                )}
              </div>
            </Field>
          )}
          {onWindowsDrive && (
            <Notice tone="info">
              {t.rich('project.add.windowsDrive', { code: (c) => <Code>{c}</Code> })}
            </Notice>
          )}
          {!creating && (
            <Field label={t('project.add.nameLabel')} htmlFor="project-name" hint={t('project.add.nameHint')}>
              <Input id="project-name" placeholder="My App" value={name} onChange={(event) => setName(event.target.value)} />
            </Field>
          )}
          {claudeAccounts.length > 1 && (
            <Field label={t('project.add.claudeAccountLabel')} htmlFor="project-claude-account" hint={t('project.add.claudeAccountHint')}>
              <Select id="project-claude-account" value={claudeAccount} onChange={setClaudeAccount}>
                <SelectOption value="">{defaultOption(claudeAccounts)}</SelectOption>
                {claudeAccounts.map((account) => (
                  <SelectOption key={account.name} value={account.name}>
                    {account.name}
                  </SelectOption>
                ))}
              </Select>
            </Field>
          )}
          {githubAccounts.length > 1 && (
            <Field
              label={t('project.add.githubAccountLabel')}
              htmlFor="project-github-account"
              hint={t('project.add.githubAccountHint')}
            >
              <Select id="project-github-account" value={githubAccount} onChange={setGitHubAccount}>
                <SelectOption value="">{defaultOption(githubAccounts)}</SelectOption>
                {githubAccounts.map((account) => (
                  <SelectOption key={account.name} value={account.name}>
                    {githubLabel(account)}
                  </SelectOption>
                ))}
              </Select>
            </Field>
          )}
          {auth.data && githubAccounts.length === 0 && (
            <p className="text-xs text-subtle">
              {t.rich('project.add.noGitHub', {
                link: (c) => (
                  <button type="button" className="font-medium text-brand-300 hover:underline" onClick={onOpenSetup}>
                    {c}
                  </button>
                ),
              })}
            </p>
          )}
          {notEmpty ? (
            <Notice tone="info">
              {t.rich('project.add.notEmpty', { code: (c) => <Code>{c}</Code>, path: newPath })}
            </Notice>
          ) : (
            add.error && <Notice>{errorMessage(add.error)}</Notice>
          )}
          <DialogFooter>
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={!(creating ? newPath : path.trim()) || add.isPending}>
              {add.isPending && <LoaderCircle className="animate-spin" />}
              {submitLabel({ creating, notEmpty, onWindowsDrive, pending: add.isPending })}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function submitLabel({ creating, notEmpty, onWindowsDrive, pending }: { creating: boolean; notEmpty: boolean; onWindowsDrive: boolean; pending: boolean }) {
  if (creating) {
    if (notEmpty) return translate('project.add.commitAndAdd');
    return pending ? translate('project.add.creating') : translate('project.add.createAndAdd');
  }
  if (onWindowsDrive) return pending ? translate('project.add.copying') : translate('project.add.copyAndAdd');
  return translate('project.add.submit');
}

// windowsDrive is where WSL mounts Windows's drives, as the daemon's
// checkProjectDisk has it.
const windowsDrive = /^\/mnt\/[a-zA-Z](\/|$)/;

// defaultOption words the account choice that follows the machine's default.
function defaultOption(accounts: { name: string; default: boolean }[]): string {
  const name = defaultOf(accounts);
  return name ? translate('project.newAgent.defaultOption', { value: name }) : translate('common.default');
}

// defaultOf is the account a project gets when it picks none.
function defaultOf(accounts: { name: string; default: boolean }[]): string | undefined {
  return accounts.find((a) => a.default)?.name;
}

// githubLabel names a GitHub account and who it is on GitHub, the way the
// Settings page lists them: the login is what tells two accounts apart. It is
// missing only for an account stored before AgentBox remembered logins.
function githubLabel(account: T.GitHubAccount): string {
  return account.login ? `${account.name} (${account.login})` : account.name;
}
