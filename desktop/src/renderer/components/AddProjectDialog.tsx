import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { FolderGit2, FolderOpen, FolderPlus, GitPullRequest, Link, LoaderCircle } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import * as T from '../../shared/api';
import { api, ApiError } from '../lib/api';
import { formatList, t as translate, useT } from '../lib/i18n';
import { joinPath, nameOf, parentOf } from '../lib/paths';
import { errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Code, Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input } from './ui/input';
import { JobProgress } from './JobProgress';
import { Select, SelectOption } from './ui/select';
import { Tabs, TabsList, TabsTrigger } from './ui/tabs';
import { ClaudeAccountState, ToolAccountState } from './ProjectAccounts';
import { AIIcon, aiLabel } from './state';
import { accountsOf } from './ToolAccounts';

// A project is a repository that's already there, one AgentBox starts (a
// new folder, git init on main and an initial commit, so agents have
// something to branch from: AddProjectRequest.create), or one it clones from
// a link (CloneProjectRequest), in a job whose log the dialog shows.
export type AddProjectMode = 'existing' | 'new' | 'clone';
type Mode = AddProjectMode;

export function AddProjectDialog({
  open,
  onOpenChange,
  onAdded,
  onOpenSetup,
  initialMode = 'existing',
}: {
  open: boolean;
  initialMode?: Mode;
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
  // A clone is a link and the folder it goes in, which starts as the clone
  // folder (Settings.cloneFolder, ~/src) and becomes it when another is used.
  const cloning = mode === 'clone';
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings, enabled: open });
  const [url, setURL] = useState('');
  const [cloneParent, setCloneParent] = useState('');
  const [cloneJob, setCloneJob] = useState<string>();
  // Only a link has a folder to show; anything else is the daemon's to refuse.
  const clonePath = looksLikeLink(url.trim()) ? joinPath(cloneParent.trim(), repoName(url.trim())) : '';
  // "" is this machine's default account, which is what a project gets until
  // it picks one; the accounts only show up when there is a choice to make.
  const [claudeAccount, setClaudeAccount] = useState('');
  const [githubAccount, setGitHubAccount] = useState('');
  const [codexAccount, setCodexAccount] = useState('');
  const [cursorAccount, setCursorAccount] = useState('');
  const [geminiAccount, setGeminiAccount] = useState('');
  const [openCodeAccounts, setOpenCodeAccounts] = useState<Record<string, string>>({});
  // The AI tool the project's agents run when they name none; "" keeps what
  // every project does without one (Project.agentTool).
  const [agentTool, setAgentTool] = useState('');
  const codexAccounts = accountsOf(auth.data, 'codex');
  const cursorAccounts = accountsOf(auth.data, 'cursor');
  const geminiAccounts = accountsOf(auth.data, 'gemini');
  const openCodeAll = accountsOf(auth.data, 'opencode');
  const providers = [...new Set(openCodeAll.map((a) => a.provider ?? ''))].filter(Boolean);
  const signedOut = [
    claudeAccounts.length === 0 && aiLabel('claude'),
    codexAccounts.length === 0 && aiLabel('codex'),
    providers.length === 0 && aiLabel('opencode'),
    cursorAccounts.length === 0 && aiLabel('cursor'),
    geminiAccounts.length === 0 && aiLabel('gemini'),
    githubAccounts.length === 0 && 'GitHub',
  ].filter((name): name is string => !!name);
  // On Windows, the picker gives a folder on a Windows drive as /mnt/<drive>/...:
  // the daemon adds a copy of it in the WSL distro's ~/src rather than the
  // folder itself, which git would be slow in there (D91).
  const onWindowsDrive = mode === 'existing' && !remote && windowsDrive.test(path.trim());
  const accounts = {
    claudeAccount: claudeAccount || undefined,
    githubAccount: githubAccount || undefined,
    codexAccount: codexAccount || undefined,
    cursorAccount: cursorAccount || undefined,
    geminiAccount: geminiAccount || undefined,
    openCodeAccounts: Object.values(openCodeAccounts).some(Boolean) ? openCodeAccounts : undefined,
  };
  const added = async (project: T.Project) => {
    if (agentTool) {
      // Set once the project is there, for the add and the clone alike. Not
      // worth losing the project over: Project settings → Agents has it too.
      project = await api.updateProject(project.name, { agentTool }).catch((err) => {
        toast.error(errorMessage(err));
        return project;
      });
    }
    await queryClient.invalidateQueries({ queryKey: ['projects'] });
    onOpenChange(false);
    onAdded(project);
    toast.success(t('project.add.added', { name: project.name }));
  };
  const add = useMutation({
    // commitFiles is the user's answer to the daemon's saying the new
    // repository's folder already has files in it. In clone mode, this is
    // adding the folder that was in the clone's way, as it is.
    mutationFn: (commitFiles: boolean) =>
      api.addProject({
        path: cloning ? clonePath : creating ? newPath : path.trim(),
        name: creating ? undefined : name.trim() || undefined,
        ...accounts,
        copyToLinux: onWindowsDrive || undefined,
        create: creating || undefined,
        commitFiles: (creating && commitFiles) || undefined,
      }),
    onSuccess: added,
  });
  const clone = useMutation({
    mutationFn: () => api.cloneProject({ url: url.trim(), path: clonePath, name: name.trim() || undefined, ...accounts }),
    onSuccess: (job) => setCloneJob(job.id),
  });
  const cloneDone = async (job: T.Job) => {
    if (job.status !== 'succeeded') return;
    const folder = cloneParent.trim();
    if (settings.data && folder !== settings.data.cloneFolder) {
      // Not worth failing the clone over: the next one starts in the old folder.
      await api.updateSettings({ cloneFolder: folder }).then(
        () => queryClient.invalidateQueries({ queryKey: ['settings'] }),
        () => {},
      );
    }
    await added(job.result as T.Project);
  };
  const cloneRunning = !!cloneJob && clone.isSuccess;

  useEffect(() => {
    if (!open) return;
    setMode(initialMode);
    setURL('');
    setCloneParent('');
    setCloneJob(undefined);
    clone.reset();
    setPath('');
    setName('');
    setParent('');
    setFolderName('');
    setClaudeAccount('');
    setGitHubAccount('');
    setCodexAccount('');
    setCursorAccount('');
    setGeminiAccount('');
    setOpenCodeAccounts({});
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

  useEffect(() => {
    if (cloning && !cloneParent && settings.data) setCloneParent(settings.data.cloneFolder);
  }, [cloning, cloneParent, settings.data]);

  // Another folder is another question: files in the last one say nothing
  // about this one.
  useEffect(() => {
    if (add.error) add.reset();
    if (clone.error) clone.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [newPath, clonePath, url]);

  // A clone that failed is tried again from the start, its log cleared.
  const cloneJobState = useQuery({ queryKey: ['job', cloneJob], queryFn: () => api.job(cloneJob!), enabled: !!cloneJob });
  const cloneFailed = !!cloneJob && !!cloneJobState.data && cloneJobState.data.status !== 'running' && cloneJobState.data.status !== 'succeeded';

  // The folder has files in it: submitting again commits them.
  const notEmpty = creating && add.error instanceof ApiError && add.error.code === T.ErrorFolderNotEmpty;
  // The clone's folder is already there: submitting again adds it as it is.
  const folderExists = cloning && clone.error instanceof ApiError && clone.error.code === T.ErrorFolderExists;
  // A link that isn't one is said under the link, where the eye is.
  const badLink = cloning && clone.error instanceof ApiError && clone.error.code === T.ErrorBadRepoURL;
  const submitting = add.isPending || clone.isPending || (cloneRunning && !cloneFailed);

  const browse = async (set: (dir: string) => void) => {
    const dir = await window.agentbox.pickDirectory();
    if (dir) set(dir);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* overflow-x-hidden: once the form scrolls, the footer's full bleed
          (-mx-6) is wider than the scrollbar leaves, by the scrollbar. */}
      <DialogContent className="overflow-x-hidden">
        <DialogHeader>
          <DialogTitle>{t('project.add.title')}</DialogTitle>
          <DialogDescription>
            {t.rich('project.add.description', {
              kind: creating ? 'new' : cloning ? 'clone' : 'existing',
              where: remote ? <span className="text-secondary">{remote}</span> : t('project.add.thisMachine'),
              code: (c) => <Code>{c}</Code>,
              branch: 'agentbox/<agent>',
            })}
          </DialogDescription>
        </DialogHeader>
        <Tabs
          value={mode}
          onValueChange={(value) => {
            if (cloneRunning && !cloneFailed) return;
            setMode(value as Mode);
            add.reset();
            clone.reset();
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
            <TabsTrigger value="clone" className="flex-1" data-add-project-clone>
              <Link />
              {t('project.add.clone')}
            </TabsTrigger>
          </TabsList>
        </Tabs>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (!cloning || folderExists) {
              add.mutate(notEmpty);
              return;
            }
            setCloneJob(undefined);
            clone.mutate();
          }}
        >
          {cloning ? (
            <fieldset className="grid min-w-0 gap-4" disabled={cloneRunning && !cloneFailed}>
              <Field label={t('project.add.urlLabel')} htmlFor="project-url" hint={t('project.add.urlHint')}>
                <Input
                  id="project-url"
                  autoFocus
                  className="font-mono text-ui"
                  placeholder="https://github.com/owner/repo"
                  spellCheck={false}
                  value={url}
                  onChange={(event) => setURL(event.target.value)}
                />
                {badLink && <Notice>{errorMessage(clone.error)}</Notice>}
              </Field>
              <Field
                label={t('project.add.cloneFolderLabel')}
                htmlFor="project-clone-folder"
                hint={clonePath ? t.rich('project.add.cloneHint', { code: (c) => <Code>{c}</Code>, path: clonePath }) : t('project.add.cloneHintEmpty')}
              >
                <div className="flex gap-2">
                  <Input
                    id="project-clone-folder"
                    className="font-mono text-ui"
                    placeholder="/home/you/src"
                    value={cloneParent}
                    onChange={(event) => setCloneParent(event.target.value)}
                  />
                  {!remote && (
                    <Button onClick={() => void browse(setCloneParent)}>
                      <FolderOpen />
                      {t('project.add.browse')}
                    </Button>
                  )}
                </div>
              </Field>
              <Field label={t('project.add.nameLabel')} htmlFor="project-clone-name" hint={t('project.add.cloneNameHint')}>
                <Input id="project-clone-name" placeholder={repoName(url.trim()) || 'my-app'} value={name} onChange={(event) => setName(event.target.value)} />
              </Field>
            </fieldset>
          ) : creating ? (
            <>
              <Field label={t('project.add.parentLabel')} htmlFor="project-parent">
                <div className="flex gap-2">
                  <Input
                    id="project-parent"
                    className="font-mono text-ui"
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
                  className="font-mono text-ui"
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
          {mode === 'existing' && (
            <Field label={t('project.add.nameLabel')} htmlFor="project-name" hint={t('project.add.nameHint')}>
              <Input id="project-name" placeholder={nameOf(path.trim()) || 'my-app'} value={name} onChange={(event) => setName(event.target.value)} />
            </Field>
          )}
          <Field label={t('project.add.toolLabel')} htmlFor="project-agent-tool" hint={t('project.add.toolHint')}>
            <Select id="project-agent-tool" value={agentTool} onChange={setAgentTool}>
              <SelectOption value="">{t('defaults.project.toolNone')}</SelectOption>
              {['claude', 'codex', 'opencode', 'cursor', 'gemini'].map((tool) => (
                <SelectOption key={tool} value={tool}>
                  {aiLabel(tool)}
                </SelectOption>
              ))}
            </Select>
          </Field>
          {auth.data && (
            <fieldset className="grid min-w-0 gap-2" data-project-accounts>
              <legend className="mb-1 text-ui font-medium text-secondary">{t('project.add.accountsLabel')}</legend>
              <p className="-mt-1 text-xs text-subtle">{t('project.add.accountsHint')}</p>
              {claudeAccounts.length > 0 && (
                <AccountRow tool="claude" label={aiLabel('claude')} accounts={claudeAccounts} value={claudeAccount} onChange={setClaudeAccount}>
                  <ClaudeAccountState chosen={claudeAccount} />
                </AccountRow>
              )}
              {codexAccounts.length > 0 && (
                <AccountRow tool="codex" label={aiLabel('codex')} accounts={codexAccounts} value={codexAccount} onChange={setCodexAccount}>
                  <ToolAccountState accounts={codexAccounts} chosen={codexAccount} />
                </AccountRow>
              )}
              {providers.map((provider) => {
                  const mine = openCodeAll.filter((a) => a.provider === provider);
                  const value = openCodeAccounts[provider] ?? '';
                  return (
                    <AccountRow
                      key={provider}
                      tool="opencode"
                      label={`${aiLabel('opencode')} · ${provider}`}
                      accounts={mine}
                      value={value}
                      onChange={(v) => setOpenCodeAccounts((was) => ({ ...was, [provider]: v }))}
                    >
                      <ToolAccountState accounts={mine} chosen={value} />
                    </AccountRow>
                  );
                })}
              {cursorAccounts.length > 0 && (
                <AccountRow tool="cursor" label={aiLabel('cursor')} accounts={cursorAccounts} value={cursorAccount} onChange={setCursorAccount}>
                  <ToolAccountState accounts={cursorAccounts} chosen={cursorAccount} />
                </AccountRow>
              )}
              {geminiAccounts.length > 0 && (
                <AccountRow tool="gemini" label={aiLabel('gemini')} accounts={geminiAccounts} value={geminiAccount} onChange={setGeminiAccount}>
                  <ToolAccountState accounts={geminiAccounts} chosen={geminiAccount} />
                </AccountRow>
              )}
              {githubAccounts.length > 0 && (
                <AccountRow tool="github" label="GitHub" accounts={githubAccounts} value={githubAccount} onChange={setGitHubAccount} named={githubLabel}>
                  <ToolAccountState
                    accounts={githubAccounts.map((a) => ({ tool: 'github', name: a.name, default: a.default, savedAt: a.savedAt, email: a.login }))}
                    chosen={githubAccount}
                  />
                </AccountRow>
              )}
              {/* The tools with no login at all are one line, not a row each
                  saying so. */}
              {signedOut.length > 0 && (
                <p className="flex min-w-0 items-start gap-1.5 text-caption leading-snug text-subtle" data-accounts-signed-out>
                  <span className="mt-[5px] size-1.5 shrink-0 rounded-full bg-faint" />
                  <span className="min-w-0 break-words">{t('project.add.notSignedIn', { tools: formatList(signedOut) })}</span>
                </p>
              )}
            </fieldset>
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
          {cloneJob && <JobProgress key={cloneJob} jobId={cloneJob} header={false} logClassName="h-40" onDone={(job) => void cloneDone(job)} />}
          {notEmpty ? (
            <Notice tone="info">
              {t.rich('project.add.notEmpty', { code: (c) => <Code>{c}</Code>, path: newPath })}
            </Notice>
          ) : folderExists ? (
            <Notice tone="info">
              {t.rich('project.add.folderExists', { code: (c) => <Code>{c}</Code>, path: clonePath })}
            </Notice>
          ) : (
            (add.error || (clone.error && !badLink)) && <Notice>{errorMessage(add.error ?? clone.error)}</Notice>
          )}
          {/* Kept in view at the bottom of the dialog, however long the form. */}
          <DialogFooter className="sticky -bottom-6 -mx-6 -mb-6 border-t border-line bg-modal px-6 py-4">
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={!(cloning ? url.trim() : creating ? newPath : path.trim()) || submitting}>
              {submitting && <LoaderCircle className="animate-spin" />}
              {submitLabel({ mode, notEmpty, folderExists, onWindowsDrive, pending: submitting, retry: cloneFailed })}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function submitLabel({
  mode,
  notEmpty,
  folderExists,
  onWindowsDrive,
  pending,
  retry,
}: {
  mode: Mode;
  notEmpty: boolean;
  folderExists: boolean;
  onWindowsDrive: boolean;
  pending: boolean;
  retry: boolean;
}) {
  if (mode === 'clone') {
    if (folderExists) return translate('project.add.addFolder');
    if (pending) return translate('project.add.cloning');
    return retry ? translate('project.add.cloneAgain') : translate('project.add.cloneAndAdd');
  }
  if (mode === 'new') {
    if (notEmpty) return translate('project.add.commitAndAdd');
    return pending ? translate('project.add.creating') : translate('project.add.createAndAdd');
  }
  if (onWindowsDrive) return pending ? translate('project.add.copying') : translate('project.add.copyAndAdd');
  return translate('project.add.submit');
}

// repoName is the repository's name in a link, the folder git clone makes
// for it: https://github.com/acme/my-app.git and git@host:acme/my-app are both
// my-app. The daemon's repoName does the same.
export function repoName(url: string): string {
  const trimmed = url.replace(/\/+$/, '');
  return trimmed.slice(Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf(':')) + 1).replace(/\.git$/, '');
}

// looksLikeLink is whether text could be a repository's link, as the
// daemon's cloneable has it: scheme://… or user@host:path.
function looksLikeLink(url: string): boolean {
  return /^[a-z][a-z0-9+.-]*:\/\/\S+\/\S/i.test(url) || /^[^\s/:\\]{2,}:[^\s]+\/?[^\s]*$/.test(url);
}

// windowsDrive is where WSL mounts Windows's drives, as the daemon's
// checkProjectDisk has it.
const windowsDrive = /^\/mnt\/[a-zA-Z](\/|$)/;

// AccountRow is one tool's account in the new project's form: a choice when
// there are accounts, and how the one it comes to stands.
function AccountRow({
  tool,
  label,
  accounts,
  value,
  onChange,
  named,
  children,
}: {
  tool: string;
  label: string;
  accounts: { name: string; default: boolean }[];
  value: string;
  onChange: (value: string) => void;
  named?: (account: never) => string;
  children: ReactNode;
}) {
  const t = useT();
  const id = `project-${label.replace(/\W+/g, '-').toLowerCase()}-account`;
  return (
    <div className="grid min-w-0 grid-cols-[9rem_minmax(0,1fr)] items-start gap-x-3 gap-y-0.5" data-account-row={tool}>
      <label htmlFor={id} className="flex min-w-0 items-start gap-1.5 pt-1.5 text-ui leading-snug text-secondary">
        {tool === 'github' ? <GitPullRequest className="mt-0.5 size-3.5 shrink-0 text-subtle" /> : <AIIcon ai={tool} className="mt-0.5 size-3.5 shrink-0 text-subtle" />}
        <span className="min-w-0 break-words">{label}</span>
      </label>
      <div className="grid min-w-0 gap-0.5">
        {accounts.length > 0 ? (
          <Select id={id} aria-label={t('project.add.accountAria', { tool: label })} value={value} onChange={onChange}>
            <SelectOption value="">{defaultOption(accounts)}</SelectOption>
            {accounts.map((account) => (
              <SelectOption key={account.name} value={account.name}>
                {named ? named(account as never) : account.name}
              </SelectOption>
            ))}
          </Select>
        ) : null}
        {children}
      </div>
    </div>
  );
}

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
