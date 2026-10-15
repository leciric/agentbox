import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, LoaderCircle } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { t as translate, useT } from '../lib/i18n';
import { cn, errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Select, SelectOption } from './ui/select';
import { SettingNote, SettingRow } from './ui/settings';
import { fiveHour } from '../lib/leadRunner';
import { aiLabel } from './state';
import { accountKind, accountsOf } from './ToolAccounts';

// The accounts a project's new agents get, from the ones stored in Settings:
// its Claude Code login, the logins it may use at all, its Codex, OpenCode and
// Cursor accounts, and its GitHub account.
// They're project settings like the rest, so they're drawn with them
// (ProjectSettings), in Settings and on the project's Overview alike.

// ClaudeAccountPicker chooses which stored Claude Code login this project's new
// agents get. Agents that already exist keep the token they were created with.
export function ClaudeAccountPicker({ project }: { project: T.Project }) {
  const t = useT();
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
          ? t('project.accounts.claudeSet', { name: updated.name, account: updated.claudeAccount })
          : t('project.accounts.claudeDefault', { name: updated.name }),
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
        label={t('project.accounts.claudeLabel')}
        description={auth.isPending ? t('common.loading') : accounts.length === 0 ? t('project.accounts.noneStored') : t('project.accounts.claudeDescription')}
        details={accounts.length > 0 && t('project.accounts.claudeDetails')}
        control={
          accounts.length > 0 && (
            <Select aria-label={t('project.accounts.claudeLabel')} value={project.claudeAccount} disabled={pick.isPending} onChange={choose}>
              <SelectOption value="">{fallback ? t('project.newAgent.defaultOption', { value: fallback }) : t('common.default')}</SelectOption>
              {accounts.filter((account) => allowed(project, account.name)).map((account) => (
                <SelectOption key={account.name} value={account.name}>
                  {account.name}
                </SelectOption>
              ))}
            </Select>
          )
        }
      >
        {accounts.length > 0 && <ClaudeAccountState chosen={project.claudeAccount} />}
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
  return name ? `"${name}"` : translate('project.accounts.theDefault');
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
  const t = useT();
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

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('project.accounts.moveTitle')}</DialogTitle>
          <DialogDescription>
            {t('project.accounts.moveQuestion', { count, from: accountLabel(from), to: accountLabel(to) })}
          </DialogDescription>
        </DialogHeader>
        {error && <Notice>{error}</Notice>}
        <DialogFooter>
          <Button variant="ghost" disabled={pending !== null} onClick={() => void choose(false)}>
            {pending === 'leave' && <LoaderCircle className="animate-spin" />}
            {t('project.accounts.leave', { count })}
          </Button>
          <Button variant="primary" disabled={pending !== null} onClick={() => void choose(true)}>
            {pending === 'move' && <LoaderCircle className="animate-spin" />}
            {t('project.accounts.move', { count })}
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
  const t = useT();
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
        label={t('project.accounts.allowedLabel')}
        description={auth.isPending ? t('common.loading') : t('project.accounts.onlyOne', { count: accounts.length })}
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
      label={t('project.accounts.allowedLabel')}
      description={t('project.accounts.allowedDescription', {
        state: project.claudeAccounts.length === 0 ? 'all' : 'some',
        allowed: project.claudeAccounts.length,
        total: accounts.length,
      })}
      details={t('project.accounts.allowedDetails')}
    >
      <div className="flex min-w-0 flex-wrap items-center gap-1.5" role="group" aria-label={t('project.accounts.allowedAria')}>
        {accounts.map((account) => {
          const on = allowed(project, account.name);
          const locked = account.name === project.claudeAccount;
          return (
            <button
              key={account.name}
              type="button"
              aria-pressed={on}
              disabled={save.isPending || (on && locked)}
              title={locked ? t('project.accounts.locked') : undefined}
              onClick={() => toggle(account.name)}
              className={cn(
                'flex h-8 min-w-0 items-center gap-1.5 rounded-lg border border-line px-2.5 text-[13px] text-muted transition hover:bg-surface hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400 disabled:cursor-default',
                on && 'border-transparent bg-surface-strong text-title shadow-[inset_0_1px_0_rgb(255_255_255/0.06)]',
              )}
            >
              {on ? <Check className="size-3.5 shrink-0" /> : <span className="size-3.5 shrink-0" />}
              <span className="truncate">{account.name}</span>
              {account.name === effective && <span className="text-subtle">{t('project.accounts.defaultChip')}</span>}
            </button>
          );
        })}
      </div>
      {effective && !allowed(project, effective) && (
        <SettingNote tone="warning">{t('project.accounts.notAllowed', { account: effective })}</SettingNote>
      )}
      {save.error && <SettingNote tone="error">{errorMessage(save.error)}</SettingNote>}
    </SettingRow>
  );
}

// GitHubAccountPicker chooses which stored GitHub login this project's new
// agents get. Agents that already exist keep the token they were created with.
export function GitHubAccountPicker({ project }: { project: T.Project }) {
  const t = useT();
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
          ? t('project.accounts.githubSet', { name: updated.name, account: updated.githubAccount })
          : t('project.accounts.githubDefault', { name: updated.name }),
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
        label={t('project.accounts.githubLabel')}
        description={auth.isPending ? t('common.loading') : accounts.length === 0 ? t('project.accounts.noneStored') : t('project.accounts.githubDescription')}
        details={accounts.length > 0 && t('project.accounts.githubDetails')}
        control={
          accounts.length > 0 && (
            <Select aria-label={t('project.accounts.githubLabel')} value={project.githubAccount} disabled={pick.isPending} onChange={choose}>
              <SelectOption value="">{fallback ? t('project.newAgent.defaultOption', { value: fallback }) : t('common.default')}</SelectOption>
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
                  {t('project.accounts.removed', { account: project.githubAccount })}
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

// The project's accounts for the other tools: Codex's and Cursor's, one each,
// and OpenCode's, one per provider. New agents on the tool get it, and the
// project's chat moves to it as its tool next starts (the daemon restarts a
// chat running that tool on it at once).

// ToolAccountPicker chooses the project's Codex or Cursor account.
export function ToolAccountPicker({ project, tool }: { project: T.Project; tool: 'codex' | 'cursor' }) {
  const t = useT();
  const queryClient = useQueryClient();
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const accounts = accountsOf(auth.data, tool);
  const chosen = tool === 'codex' ? project.codexAccount : project.cursorAccount;
  const fallback = accounts.find((a) => a.default)?.name;
  const label = t(tool === 'codex' ? 'project.accounts.codexLabel' : 'project.accounts.cursorLabel');
  const pick = useMutation({
    mutationFn: (account: string) => api.updateProject(project.name, tool === 'codex' ? { codexAccount: account } : { cursorAccount: account }),
    onSuccess: async (updated, account) => {
      toast(
        account
          ? t('project.accounts.toolSet', { tool: aiLabel(tool), name: updated.name, account })
          : t('project.accounts.toolDefault', { tool: aiLabel(tool), name: updated.name }),
      );
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
      await queryClient.invalidateQueries({ queryKey: ['chatRunners', project.name] });
    },
  });
  return (
    <SettingRow
      label={label}
      description={auth.isPending ? t('common.loading') : t(tool === 'codex' ? 'project.accounts.codexDescription' : 'project.accounts.cursorDescription')}
      details={accounts.length > 0 && t('project.accounts.toolDetails')}
      control={
        accounts.length > 0 && (
          <Select aria-label={label} value={chosen} disabled={pick.isPending} onChange={(v) => pick.mutate(v)}>
            <SelectOption value="">{fallback ? t('project.newAgent.defaultOption', { value: fallback }) : t('common.default')}</SelectOption>
            {accounts.map((account) => (
              <SelectOption key={account.name} value={account.name}>
                {account.name}
              </SelectOption>
            ))}
            {chosen && !accounts.some((a) => a.name === chosen) && (
              <SelectOption value={chosen} disabled>
                {t('project.accounts.removed', { account: chosen })}
              </SelectOption>
            )}
          </Select>
        )
      }
    >
      {!auth.isPending && <ToolAccountState accounts={accounts} chosen={chosen} />}
      {pick.error && <SettingNote tone="error">{errorMessage(pick.error)}</SettingNote>}
    </SettingRow>
  );
}

// OpenCodeAccountsPicker chooses the project's OpenCode account for each
// provider that has one.
export function OpenCodeAccountsPicker({ project }: { project: T.Project }) {
  const t = useT();
  const queryClient = useQueryClient();
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const accounts = accountsOf(auth.data, 'opencode');
  const providers = [...new Set(accounts.map((a) => a.provider ?? ''))].filter(Boolean);
  const chosen = project.openCodeAccounts ?? {};
  const pick = useMutation({
    mutationFn: (next: Record<string, string>) => api.updateProject(project.name, { openCodeAccounts: next }),
    onSuccess: async () => {
      toast(t('project.accounts.openCodeSet', { name: project.name }));
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
      await queryClient.invalidateQueries({ queryKey: ['chatRunners', project.name] });
    },
  });
  return (
    <SettingRow
      label={t('project.accounts.openCodeLabel')}
      description={auth.isPending ? t('common.loading') : t('project.accounts.openCodeDescription')}
      details={providers.length > 0 && t('project.accounts.toolDetails')}
    >
      {providers.length === 0 && !auth.isPending && <ToolAccountState accounts={[]} chosen="" />}
      {providers.length > 0 && (
        <div className="grid min-w-0 gap-1.5" data-opencode-accounts>
          {providers.map((provider) => {
            const mine = accounts.filter((a) => a.provider === provider);
            const fallback = mine.find((a) => a.default)?.name;
            const value = chosen[provider] ?? '';
            return (
              <div key={provider} className="flex min-w-0 flex-wrap items-center gap-2">
                <span className="w-24 shrink-0 truncate font-mono text-[12px] text-secondary">{provider}</span>
                <div className="w-48 min-w-0">
                  <Select
                    aria-label={t('project.accounts.providerAria', { provider })}
                    value={value}
                    disabled={pick.isPending}
                    onChange={(v) => pick.mutate({ ...chosen, [provider]: v })}
                  >
                    <SelectOption value="">{fallback ? t('project.newAgent.defaultOption', { value: fallback }) : t('common.default')}</SelectOption>
                    {mine.map((account) => (
                      <SelectOption key={account.name} value={account.name}>
                        {account.name}
                      </SelectOption>
                    ))}
                  </Select>
                </div>
                <ToolAccountState accounts={mine} chosen={value} inline />
              </div>
            );
          })}
        </div>
      )}
      {pick.error && <SettingNote tone="error">{errorMessage(pick.error)}</SettingNote>}
    </SettingRow>
  );
}

// ToolAccountState says how the account a choice comes to stands: signed in
// (how, and as whom), expired, gone, or none at all.
export function ToolAccountState({ accounts, chosen, inline }: { accounts: T.ToolAccount[]; chosen: string; inline?: boolean }) {
  const t = useT();
  const on = chosen ? accounts.find((a) => a.name === chosen) : accounts.find((a) => a.default);
  let tone: 'ok' | 'warning' | 'muted' = 'ok';
  let text: string;
  if (accounts.length === 0) {
    tone = 'muted';
    text = t('project.accounts.notSignedIn');
  } else if (!on) {
    tone = 'warning';
    text = t('project.accounts.goneState', { account: chosen });
  } else if (on.expired) {
    tone = 'warning';
    text = t('project.accounts.expiredState', { account: on.name });
  } else {
    const kind = accountKind(t, on);
    text = t('project.accounts.signedIn', { account: on.name }) + (kind ? ` · ${kind}` : '');
  }
  return (
    <span
      className={cn('flex min-w-0 items-start gap-1.5 text-[12px] leading-snug', !inline && 'mt-1', tone === 'warning' ? 'text-amber-300' : 'text-subtle')}
      data-account-state={tone}
    >
      <span className={cn('mt-[5px] size-1.5 shrink-0 rounded-full', tone === 'ok' ? 'bg-emerald-400' : tone === 'warning' ? 'bg-amber-400' : 'bg-faint')} />
      <span className="min-w-0 break-words">{text}</span>
    </span>
  );
}

// ClaudeAccountState is the same for Claude Code, with what Anthropic last
// said about the token and how much of its 5-hour limit is used.
export function ClaudeAccountState({ chosen }: { chosen: string }) {
  const t = useT();
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const limits = useQuery({ queryKey: ['claudeLimits'], queryFn: api.claudeLimits, staleTime: 30_000 });
  const accounts = auth.data?.claudeAccounts ?? [];
  const on = chosen ? accounts.find((a) => a.name === chosen) : accounts.find((a) => a.default);
  if (auth.isPending) return null;
  if (!on) {
    return accounts.length === 0 ? (
      <ToolAccountState accounts={[]} chosen="" />
    ) : (
      <ToolAccountState accounts={[{ tool: 'claude', name: '', default: false, savedAt: '' }]} chosen={chosen} />
    );
  }
  const used = fiveHour((limits.data ?? []).find((l) => l.account === on.name));
  const parts = [t('project.accounts.signedIn', { account: on.name })];
  if (used !== null) parts.push(t('project.accounts.usage', { percent: Math.round(used * 100) }));
  const rejected = on.valid === 'rejected';
  return (
    <span className={cn('mt-1 flex min-w-0 items-start gap-1.5 text-[12px] leading-snug', rejected ? 'text-amber-300' : 'text-subtle')} data-account-state={rejected ? 'warning' : 'ok'}>
      <span className={cn('mt-[5px] size-1.5 shrink-0 rounded-full', rejected ? 'bg-amber-400' : 'bg-emerald-400')} />
      <span className="min-w-0 break-words">{rejected ? t('project.accounts.rejectedState', { account: on.name }) : parts.join(' · ')}</span>
    </span>
  );
}
