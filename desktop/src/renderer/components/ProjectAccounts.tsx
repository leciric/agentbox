import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, LoaderCircle } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { cn, errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Select, SelectOption } from './ui/select';
import { SettingNote, SettingRow } from './ui/settings';

// The accounts a project's new agents get, from the ones stored in Settings:
// its Claude Code login, the logins it may use at all, and its GitHub account.
// They're project settings like the rest, so they're drawn with them
// (ProjectSettings), in Settings and on the project's Overview alike.

// ClaudeAccountPicker chooses which stored Claude Code login this project's new
// agents get. Agents that already exist keep the token they were created with.
export function ClaudeAccountPicker({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const fleet = useQuery({ queryKey: ['fleet', project.name], queryFn: () => api.fleet(project.name) });
  const accounts = auth.data?.claudeAccounts ?? [];
  const fallback = accounts.find((a) => a.default)?.name;
  const [confirm, setConfirm] = useState<{ account: string; from: string; count: number } | null>(null);
  const pick = useMutation({
    mutationFn: ({ claudeAccount, moveClaudeAgents }: { claudeAccount: string; moveClaudeAgents?: boolean }) =>
      api.updateProject(project.name, { claudeAccount, moveClaudeAgents }),
    onSuccess: async (updated) => {
      toast(
        updated.claudeAccount
          ? `New agents of ${updated.name} use the Claude Code account "${updated.claudeAccount}"`
          : `New agents of ${updated.name} use this machine's default Claude Code account`,
      );
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
      await queryClient.invalidateQueries({ queryKey: ['fleet', project.name] });
    },
  });

  const choose = (value: string) => {
    if (value === project.claudeAccount) {
      pick.mutate({ claudeAccount: value });
      return;
    }
    const from = project.claudeAccount || fallback || '';
    const count = (fleet.data?.agents ?? []).filter((a) => a.ai === 'claude' && a.claudeAccount === from).length;
    if (count === 0) {
      pick.mutate({ claudeAccount: value });
      return;
    }
    setConfirm({ account: value, from, count });
  };

  return (
    <>
      <SettingRow
        label="Claude Code account"
        description={
          auth.isPending
            ? 'Loading…'
            : accounts.length === 0
              ? 'No account stored yet — add one in Settings → Accounts.'
              : 'The Claude Code login its new agents get.'
        }
        details={accounts.length > 0 && 'Agents that already exist keep the token they were created with. Changing it asks whether to move the agents still on the old one too.'}
        control={
          accounts.length > 0 && (
            <Select aria-label="Claude Code account" value={project.claudeAccount} disabled={pick.isPending} onChange={choose}>
              <SelectOption value="">Default{fallback ? ` (${fallback})` : ''}</SelectOption>
              {accounts.filter((account) => allowed(project, account.name)).map((account) => (
                <SelectOption key={account.name} value={account.name}>
                  {account.name}
                </SelectOption>
              ))}
            </Select>
          )
        }
      >
        {pick.error && <SettingNote tone="error">{errorMessage(pick.error)}</SettingNote>}
      </SettingRow>
      {confirm && (
        <MoveAgentsDialog
          open
          onOpenChange={(open) => {
            if (!open) setConfirm(null);
          }}
          count={confirm.count}
          from={confirm.from}
          to={confirm.account || fallback || ''}
          onChoose={(move) => pick.mutateAsync({ claudeAccount: confirm.account, moveClaudeAgents: move })}
        />
      )}
    </>
  );
}

function allowed(project: T.Project, name: string) {
  return project.claudeAccounts.length === 0 || project.claudeAccounts.includes(name);
}

// accountLabel names an account the way a question reads best: quoted, or
// "the default account" for the machine's own.
function accountLabel(name: string) {
  return name ? `"${name}"` : 'the default account';
}

// MoveAgentsDialog asks whether a project's agents still on the account
// being replaced should move to the new one too, or stay where they are.
// Both choices go ahead with the account change; only whether the agents
// come with it differs, so this isn't a Cancel/Confirm ConfirmDialog.
function MoveAgentsDialog({
  open,
  onOpenChange,
  count,
  from,
  to,
  onChoose,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  count: number;
  from: string;
  to: string;
  onChoose: (move: boolean) => Promise<unknown>;
}) {
  const [pending, setPending] = useState<'move' | 'leave' | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (open) {
      setPending(null);
      setError(null);
    }
  }, [open]);

  const choose = async (move: boolean) => {
    setPending(move ? 'move' : 'leave');
    setError(null);
    try {
      await onChoose(move);
      onOpenChange(false);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setPending(null);
    }
  };

  const plural = count === 1 ? '' : 's';
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Move its agents too?</DialogTitle>
          <DialogDescription>
            {count} agent{plural} still {count === 1 ? 'uses' : 'use'} {accountLabel(from)}. Move {count === 1 ? 'it' : 'them'} to {accountLabel(to)} too, or
            leave {count === 1 ? 'it' : 'them'} where {count === 1 ? 'it is' : 'they are'}?
          </DialogDescription>
        </DialogHeader>
        {error && <Notice>{error}</Notice>}
        <DialogFooter>
          <Button variant="ghost" disabled={pending !== null} onClick={() => void choose(false)}>
            {pending === 'leave' && <LoaderCircle className="animate-spin" />}
            Leave them
          </Button>
          <Button variant="primary" disabled={pending !== null} onClick={() => void choose(true)}>
            {pending === 'move' && <LoaderCircle className="animate-spin" />}
            Move {count} agent{plural} too
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ClaudeAccountsPicker limits which of the machine's accounts this project's
// agents may use; none ticked off means every one. The project's own
// account can't be left out, so its chip is locked.
export function ClaudeAccountsPicker({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const accounts = auth.data?.claudeAccounts ?? [];
  const fallback = accounts.find((a) => a.default)?.name;
  const save = useMutation({
    mutationFn: (claudeAccounts: string[]) => api.updateProject(project.name, { claudeAccounts }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
  });

  if (accounts.length < 2) {
    return (
      <SettingRow
        label="Allowed accounts"
        description={auth.isPending ? 'Loading…' : `Every account — there is only ${accounts.length === 1 ? 'one' : 'none'} on this machine.`}
      />
    );
  }
  const toggle = (name: string) => {
    const current = accounts.map((a) => a.name).filter((n) => allowed(project, n));
    const next = current.includes(name) ? current.filter((n) => n !== name) : [...current, name];
    if (next.length === 0) return;
    save.mutate(next.length === accounts.length ? [] : next);
  };
  const effective = project.claudeAccount || fallback;
  return (
    <SettingRow
      label="Allowed accounts"
      description={
        <>
          Which Claude Code accounts its agents may use.{' '}
          {project.claudeAccounts.length === 0 ? 'Every account allowed.' : `${project.claudeAccounts.length} of ${accounts.length} allowed.`}
        </>
      }
      details="Agents that already have an account keep it. The account its new agents get is always allowed."
    >
      <div className="flex min-w-0 flex-wrap items-center gap-1.5" role="group" aria-label="Allowed Claude Code accounts">
        {accounts.map((account) => {
          const on = allowed(project, account.name);
          const locked = account.name === project.claudeAccount;
          return (
            <button
              key={account.name}
              type="button"
              aria-pressed={on}
              disabled={save.isPending || (on && locked)}
              title={locked ? "The project's own account: pick another one above to leave it out" : undefined}
              onClick={() => toggle(account.name)}
              className={cn(
                'flex h-8 min-w-0 items-center gap-1.5 rounded-lg border border-line px-2.5 text-[13px] text-muted transition hover:bg-surface hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50 disabled:cursor-default',
                on && 'border-transparent bg-surface-strong text-title shadow-[inset_0_1px_0_rgb(255_255_255/0.06)]',
              )}
            >
              {on ? <Check className="size-3.5 shrink-0" /> : <span className="size-3.5 shrink-0" />}
              <span className="truncate">{account.name}</span>
              {account.name === effective && <span className="text-subtle">default</span>}
            </button>
          );
        })}
      </div>
      {effective && !allowed(project, effective) && (
        <SettingNote tone="warning">New agents default to {effective}, which isn't allowed: pick an allowed account above.</SettingNote>
      )}
      {save.error && <SettingNote tone="error">{errorMessage(save.error)}</SettingNote>}
    </SettingRow>
  );
}

// GitHubAccountPicker chooses which stored GitHub login this project's new
// agents get. Agents that already exist keep the token they were created with.
export function GitHubAccountPicker({ project }: { project: T.Project }) {
  const queryClient = useQueryClient();
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const fleet = useQuery({ queryKey: ['fleet', project.name], queryFn: () => api.fleet(project.name) });
  const accounts = auth.data?.githubAccounts ?? [];
  const fallback = accounts.find((a) => a.default)?.name;
  const [confirm, setConfirm] = useState<{ account: string; from: string; count: number } | null>(null);
  const pick = useMutation({
    mutationFn: ({ githubAccount, moveGitHubAgents }: { githubAccount: string; moveGitHubAgents?: boolean }) =>
      api.updateProject(project.name, { githubAccount, moveGitHubAgents }),
    onSuccess: async (updated) => {
      toast(
        updated.githubAccount
          ? `New agents of ${updated.name} use the GitHub account "${updated.githubAccount}"`
          : `New agents of ${updated.name} use this machine's default GitHub account`,
      );
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
      // What the Pull requests tab and the fleet are showing was read with the
      // other account, and the daemon has just dropped it: a repository that
      // one couldn't see may be this one's everyday repository. Without this
      // they keep the old answer until their next poll, which is a minute.
      await queryClient.invalidateQueries({ queryKey: ['pulls', project.name] });
      await queryClient.invalidateQueries({ queryKey: ['fleet', project.name] });
    },
  });

  const choose = (value: string) => {
    if (value === project.githubAccount) {
      pick.mutate({ githubAccount: value });
      return;
    }
    const from = project.githubAccount || fallback || '';
    const count = (fleet.data?.agents ?? []).filter((a) => a.githubAccount === from).length;
    if (count === 0) {
      pick.mutate({ githubAccount: value });
      return;
    }
    setConfirm({ account: value, from, count });
  };

  return (
    <>
      <SettingRow
        label="GitHub account"
        description={
          auth.isPending
            ? 'Loading…'
            : accounts.length === 0
              ? 'No account stored yet — add one in Settings → Accounts.'
              : 'The login its new agents push and open pull requests with.'
        }
        details={accounts.length > 0 && 'Agents get it as GH_TOKEN, and AgentBox uses it to show their pull requests. Changing it asks whether to move the agents still on the old one too.'}
        control={
          accounts.length > 0 && (
            <Select aria-label="GitHub account" value={project.githubAccount} disabled={pick.isPending} onChange={choose}>
              <SelectOption value="">Default{fallback ? ` (${fallback})` : ''}</SelectOption>
              {/* Every stored account: the project's allow-list is for Claude Code
                  accounts, and filtering these by it left only Default (and the
                  picked account showing as "Select…") on a project that has one. */}
              {accounts.map((account) => (
                <SelectOption key={account.name} value={account.name}>
                  {account.name}
                </SelectOption>
              ))}
              {/* An account the project picked and that was removed since: it is
                  still what the project names, so it shows rather than "Select…". */}
              {project.githubAccount && !accounts.some((a) => a.name === project.githubAccount) && (
                <SelectOption value={project.githubAccount} disabled>
                  {project.githubAccount} (removed)
                </SelectOption>
              )}
            </Select>
          )
        }
      >
        {pick.error && <SettingNote tone="error">{errorMessage(pick.error)}</SettingNote>}
      </SettingRow>
      {confirm && (
        <MoveAgentsDialog
          open
          onOpenChange={(open) => {
            if (!open) setConfirm(null);
          }}
          count={confirm.count}
          from={confirm.from}
          to={confirm.account || fallback || ''}
          onChoose={(move) => pick.mutateAsync({ githubAccount: confirm.account, moveGitHubAgents: move })}
        />
      )}
    </>
  );
}
