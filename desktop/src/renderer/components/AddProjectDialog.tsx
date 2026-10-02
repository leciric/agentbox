import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { FolderGit2, FolderOpen, FolderPlus, LoaderCircle } from 'lucide-react';
import { useEffect, useState } from 'react';
import * as T from '../../shared/api';
import { api, ApiError } from '../lib/api';
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
          <DialogTitle>Add a project</DialogTitle>
          <DialogDescription>
            {creating ? 'A new' : 'A'} git repository on {remote ? <span className="text-secondary">{remote}</span> : 'this machine'}. Each agent works in its own worktree, on a branch named{' '}
            <Code>agentbox/&lt;agent&gt;</Code> (a prefix you can change in the project's settings), and your checkout isn't touched.
          </DialogDescription>
        </DialogHeader>
        <Tabs
          value={mode}
          onValueChange={(value) => {
            setMode(value as Mode);
            add.reset();
          }}
        >
          <TabsList aria-label="Where the repository comes from" className="w-full">
            <TabsTrigger value="existing" className="flex-1">
              <FolderGit2 />
              Existing repository
            </TabsTrigger>
            <TabsTrigger value="new" className="flex-1">
              <FolderPlus />
              New repository
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
              <Field label="Parent folder" htmlFor="project-parent">
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
                      Browse…
                    </Button>
                  )}
                </div>
              </Field>
              <Field
                label="Name"
                htmlFor="project-folder-name"
                hint={
                  newPath ? (
                    <>
                      AgentBox makes <Code>{newPath}</Code>, runs <Code>git init</Code> on <Code>main</Code> with an initial commit, and adds it. A
                      repository that's already there is added as it is.
                    </>
                  ) : (
                    "The new folder's name, and the project's."
                  )
                }
              >
                <Input id="project-folder-name" autoFocus placeholder="my-app" value={folderName} onChange={(event) => setFolderName(event.target.value)} />
              </Field>
            </>
          ) : (
            <Field label="Repository folder" htmlFor="project-path">
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
                    Browse…
                  </Button>
                )}
              </div>
            </Field>
          )}
          {onWindowsDrive && (
            <Notice tone="info">
              This folder is on a Windows drive, where git is slow from WSL. AgentBox copies it into its WSL distro, under{' '}
              <Code>~/src</Code>, and adds the copy. Only what's committed is copied; the copy keeps its{' '}
              <Code>origin</Code>, and the Windows folder becomes its <Code>windows</Code> remote.
            </Notice>
          )}
          {!creating && (
            <Field label="Name" htmlFor="project-name" hint="Optional: anything you like, shown wherever the project is. Defaults to the folder's name.">
              <Input id="project-name" placeholder="My App" value={name} onChange={(event) => setName(event.target.value)} />
            </Field>
          )}
          {claudeAccounts.length > 1 && (
            <Field label="Claude Code account" htmlFor="project-claude-account" hint="Which login this project's agents work as. Only new agents.">
              <Select id="project-claude-account" value={claudeAccount} onChange={setClaudeAccount}>
                <SelectOption value="">Default{defaultOf(claudeAccounts) ? ` (${defaultOf(claudeAccounts)})` : ''}</SelectOption>
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
              label="GitHub account"
              htmlFor="project-github-account"
              hint="Which GitHub user its agents are, and whose pull requests the project reads."
            >
              <Select id="project-github-account" value={githubAccount} onChange={setGitHubAccount}>
                <SelectOption value="">Default{defaultOf(githubAccounts) ? ` (${defaultOf(githubAccounts)})` : ''}</SelectOption>
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
              No GitHub account yet, so this project's agents get no GitHub token and its pull requests can't be read.{' '}
              <button type="button" className="font-medium text-brand-300 hover:underline" onClick={onOpenSetup}>
                Add one in Settings
              </button>
              .
            </p>
          )}
          {notEmpty ? (
            <Notice tone="info">
              <Code>{newPath}</Code> already has files in it and no commits. AgentBox can make them the repository's initial commit, leaving out what a{' '}
              <Code>.gitignore</Code> there leaves out: check nothing in it should stay out of git, like a <Code>.env</Code>, first.
            </Notice>
          ) : (
            add.error && <Notice>{errorMessage(add.error)}</Notice>
          )}
          <DialogFooter>
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
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
    if (notEmpty) return 'Commit files and add';
    return pending ? 'Creating…' : 'Create and add';
  }
  if (onWindowsDrive) return pending ? 'Copying into WSL…' : 'Copy and add';
  return 'Add project';
}

// windowsDrive is where WSL mounts Windows's drives, as the daemon's
// checkProjectDisk has it.
const windowsDrive = /^\/mnt\/[a-zA-Z](\/|$)/;

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
