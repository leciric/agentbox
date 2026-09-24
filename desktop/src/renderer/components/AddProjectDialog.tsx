import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { FolderOpen, LoaderCircle } from 'lucide-react';
import { useEffect, useState } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Code, Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input } from './ui/input';
import { Select, SelectOption } from './ui/select';

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
  const [path, setPath] = useState('');
  const [name, setName] = useState('');
  // "" is this machine's default account, which is what a project gets until
  // it picks one; the accounts only show up when there is a choice to make.
  const [claudeAccount, setClaudeAccount] = useState('');
  const [githubAccount, setGitHubAccount] = useState('');
  // On Windows, the picker gives a folder on a Windows drive as /mnt/<drive>/...:
  // the daemon adds a copy of it in the WSL distro's ~/src rather than the
  // folder itself, which git would be slow in there (D91).
  const onWindowsDrive = !remote && windowsDrive.test(path.trim());
  const copyName = name.trim() || path.trim().replace(/\/+$/, '').split('/').pop() || '<name>';
  const add = useMutation({
    mutationFn: () =>
      api.addProject({
        path: path.trim(),
        name: name.trim() || undefined,
        claudeAccount: claudeAccount || undefined,
        githubAccount: githubAccount || undefined,
        copyToLinux: onWindowsDrive || undefined,
      }),
    onSuccess: async (project) => {
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
      onOpenChange(false);
      onAdded(project);
    },
  });

  useEffect(() => {
    if (!open) return;
    setPath('');
    setName('');
    setClaudeAccount('');
    setGitHubAccount('');
    add.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const browse = async () => {
    const dir = await window.agentbox.pickDirectory();
    if (dir) setPath(dir);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add a project</DialogTitle>
          <DialogDescription>
            A git repository on {remote ? <span className="text-secondary">{remote}</span> : 'this machine'}. Each agent works in its own worktree, on a branch named{' '}
            <Code>agentbox/&lt;agent&gt;</Code> (a prefix you can change in the project's settings), and your checkout isn't touched.
          </DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            add.mutate();
          }}
        >
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
                <Button onClick={() => void browse()}>
                  <FolderOpen />
                  Browse…
                </Button>
              )}
            </div>
          </Field>
          {onWindowsDrive && (
            <Notice tone="info">
              This folder is on a Windows drive, where git is slow from WSL. AgentBox copies it into its WSL distro, to{' '}
              <Code>~/src/{copyName}</Code>, and adds the copy. Only what's committed is copied; the copy keeps its{' '}
              <Code>origin</Code>, and the Windows folder becomes its <Code>windows</Code> remote.
            </Notice>
          )}
          <Field label="Name" htmlFor="project-name" hint="Optional. Defaults to the folder name.">
            <Input id="project-name" placeholder="my-app" value={name} onChange={(event) => setName(event.target.value)} />
          </Field>
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
          {add.error && <Notice>{errorMessage(add.error)}</Notice>}
          <DialogFooter>
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={!path.trim() || add.isPending}>
              {add.isPending && <LoaderCircle className="animate-spin" />}
              {onWindowsDrive ? (add.isPending ? 'Copying into WSL…' : 'Copy and add') : 'Add project'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
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
