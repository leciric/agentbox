import { useMutation, useQueryClient } from '@tanstack/react-query';
import { KeyRound, LoaderCircle, Pencil, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatList, useT, type MessageKey } from '../lib/i18n';
import { errorMessage } from '../lib/utils';
import { aiLabel } from './state';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Code, Notice } from './ui/card';
import { Input, Label } from './ui/input';

// Codex, OpenCode and Cursor logins are named accounts, like Claude Code's and
// GitHub's (internal/credentials/toolaccounts.go). These are their lists in
// Settings → Accounts, and the form that adds one from an API key; a ChatGPT
// or OAuth sign-in runs the tool's own login, which only a terminal can.

// accountsOf is a tool's stored accounts, from GET /v1/auth.
export function accountsOf(auth: T.AuthStatus | undefined, tool: string): T.ToolAccount[] {
  if (!auth) return [];
  return (tool === 'codex' ? auth.codexAccounts : tool === 'opencode' ? auth.openCodeAccounts : tool === 'cursor' ? auth.cursorAccounts : undefined) ?? [];
}

// accountKind says how an account signs in, and whose it is.
export function accountKind(t: ReturnType<typeof useT>, account: T.ToolAccount): string {
  const key = account.kind ? kindKeys[account.kind] : undefined;
  const kind = key ? t(key) : (account.kind ?? '');
  return [kind, account.email].filter(Boolean).join(' · ');
}

const kindKeys: Record<string, MessageKey> = {
  apikey: 'settings.toolAccounts.kindAPIKey',
  chatgpt: 'settings.toolAccounts.kindChatGPT',
  oauth: 'settings.toolAccounts.kindOAuth',
  wellknown: 'settings.toolAccounts.kindWellKnown',
};

// ToolAccountList lists a tool's stored accounts: each one's sign-in, the
// default, and renaming and removing them. OpenCode's are by provider.
export function ToolAccountList({ tool, accounts }: { tool: string; accounts: T.ToolAccount[] }) {
  const t = useT();
  const queryClient = useQueryClient();
  const refresh = () =>
    Promise.all(['auth', 'setup', 'settings', 'projects', 'chatRunners'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
  const key = (a: T.ToolAccount) => `${a.provider ?? ''}/${a.name}`;
  const makeDefault = useMutation({
    mutationFn: (a: T.ToolAccount) => api.setDefaultToolAccount(tool, a.name, a.provider),
    onSuccess: async (_, a) => {
      toast(t('settings.accounts.newAgentsUse', { name: a.name }));
      await refresh();
    },
  });
  const [renaming, setRenaming] = useState<string | null>(null);
  const [newName, setNewName] = useState('');
  const rename = useMutation({
    mutationFn: ({ from, to }: { from: T.ToolAccount; to: string }) => api.renameToolAccount(tool, from.name, to, from.provider),
    onSuccess: async (got) => {
      setRenaming(null);
      const carried = [
        got.projects.length > 0 && t('settings.accounts.nProjects', { count: got.projects.length }),
        got.agents.length > 0 && t('settings.accounts.nAgents', { count: got.agents.length }),
      ].filter((x): x is string => !!x);
      toast(t('settings.accounts.renamed', { old: got.old, name: got.name }), {
        description: (carried.length > 0 ? `${t('settings.accounts.movedWithIt', { what: formatList(carried) })} ` : '') + t('settings.toolAccounts.keepLogin'),
      });
      await Promise.all([refresh(), queryClient.invalidateQueries({ queryKey: ['agents'] }), queryClient.invalidateQueries({ queryKey: ['fleet'] })]);
    },
  });
  const remove = useMutation({
    mutationFn: (a: T.ToolAccount) => api.removeToolAccount(tool, a.name, a.provider),
    onSuccess: async (_, a) => {
      toast(t('settings.toolAccounts.removed', { tool: aiLabel(tool), name: a.name }), { description: t('settings.toolAccounts.removedNote') });
      await refresh();
    },
  });
  const error = makeDefault.error ?? remove.error ?? rename.error;
  if (accounts.length === 0) return null;
  return (
    <div className="grid gap-2">
      <ul className="grid gap-1.5" aria-label={t('settings.toolAccounts.list', { tool: aiLabel(tool) })}>
        {accounts.map((acc) => (
          <li
            key={key(acc)}
            data-tool-account={`${tool}/${key(acc)}`}
            className="flex flex-wrap items-center gap-2 rounded-xl border border-line bg-surface-faint py-1.5 pl-3 pr-1.5"
          >
            <KeyRound className="size-3.5 shrink-0 text-subtle" />
            {acc.provider && <span className="shrink-0 text-[12px] text-subtle">{acc.provider}</span>}
            {renaming === key(acc) ? (
              <form
                className="flex min-w-0 items-center gap-1"
                onSubmit={(event) => {
                  event.preventDefault();
                  const to = newName.trim();
                  if (to === acc.name) setRenaming(null);
                  else rename.mutate({ from: acc, to });
                }}
              >
                <Input
                  aria-label={t('settings.accounts.newNameFor', { name: acc.name })}
                  value={newName}
                  onChange={(event) => setNewName(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === 'Escape') setRenaming(null);
                  }}
                  disabled={rename.isPending}
                  autoFocus
                  className="h-7 w-32 font-mono text-[12.5px]"
                />
                <Button type="submit" size="sm" disabled={rename.isPending || newName.trim() === ''}>
                  {t('common.rename')}
                </Button>
                <Button type="button" size="sm" variant="ghost" disabled={rename.isPending} onClick={() => setRenaming(null)}>
                  {t('common.cancel')}
                </Button>
              </form>
            ) : (
              <span className="truncate font-mono text-[12.5px] text-secondary">{acc.name}</span>
            )}
            <span className="min-w-0 truncate text-[12px] text-subtle">{accountKind(t, acc)}</span>
            {acc.default && <Badge variant="brand">{t('settings.accounts.badgeDefault')}</Badge>}
            {acc.expired && <Badge variant="danger">{t('settings.toolAccounts.expired')}</Badge>}
            <div className="ml-auto flex items-center gap-1">
              {!acc.default && (
                <Button size="sm" variant="ghost" disabled={makeDefault.isPending} onClick={() => makeDefault.mutate(acc)}>
                  {t('settings.accounts.makeDefault')}
                </Button>
              )}
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label={t('settings.accounts.renameAccount', { name: acc.name })}
                disabled={rename.isPending}
                onClick={() => {
                  rename.reset();
                  setRenaming(key(acc));
                  setNewName(acc.name);
                }}
              >
                <Pencil />
              </Button>
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label={t('settings.accounts.removeAccount', { name: acc.name })}
                disabled={remove.isPending}
                onClick={() => remove.mutate(acc)}
              >
                <Trash2 />
              </Button>
            </div>
          </li>
        ))}
      </ul>
      {error && <Notice>{errorMessage(error)}</Notice>}
    </div>
  );
}

// ToolKeyForm adds a Codex or OpenCode account from an API key: OpenAI's for
// Codex, a provider's for OpenCode.
export function ToolKeyForm({ tool, accounts }: { tool: 'codex' | 'opencode'; accounts: T.ToolAccount[] }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [account, setAccount] = useState('');
  const [provider, setProvider] = useState('');
  const [key, setKey] = useState('');
  const save = useMutation({
    mutationFn: () => api.saveToolAccount(tool, { account: account.trim() || undefined, provider: provider.trim() || undefined, apiKey: key.trim() }),
    onSuccess: async () => {
      toast(t('settings.toolAccounts.saved', { tool: aiLabel(tool), account: account.trim() || 'default' }));
      setKey('');
      setAccount('');
      await Promise.all(['auth', 'setup', 'settings', 'chatRunners'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
    },
  });
  const id = `${tool}-account`;
  const ready = key.trim() !== '' && (tool !== 'opencode' || provider.trim() !== '');
  return (
    <form
      className="grid gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        if (ready) save.mutate();
      }}
    >
      <p className="text-[13px] text-muted">{t.rich(tool === 'codex' ? 'settings.toolAccounts.introCodex' : 'settings.toolAccounts.introOpenCode', { code: (c) => <Code>{c}</Code> })}</p>
      <div className="flex flex-wrap items-end gap-2">
        {tool === 'opencode' && (
          <div className="grid gap-1.5">
            <Label htmlFor={`${id}-provider`}>{t('settings.toolAccounts.provider')}</Label>
            <Input
              id={`${id}-provider`}
              list={`${id}-providers`}
              placeholder="anthropic"
              value={provider}
              onChange={(event) => setProvider(event.target.value)}
              className="w-32 font-mono"
            />
            <datalist id={`${id}-providers`}>
              {[...new Set(['anthropic', 'openai', 'google', 'openrouter', ...accounts.map((a) => a.provider ?? '')])].filter(Boolean).map((p) => (
                <option key={p} value={p} />
              ))}
            </datalist>
          </div>
        )}
        <div className="grid gap-1.5">
          <Label htmlFor={`${id}-name`}>{t('settings.accounts.account')}</Label>
          <Input
            id={`${id}-name`}
            placeholder={accounts.length === 0 ? 'default' : 'work'}
            value={account}
            onChange={(event) => setAccount(event.target.value)}
            className="w-28 font-mono"
          />
        </div>
        <div className="grid min-w-48 flex-1 gap-1.5">
          <Label htmlFor={`${id}-key`}>{t('settings.toolAccounts.apiKey')}</Label>
          <Input
            id={`${id}-key`}
            type="password"
            autoComplete="off"
            spellCheck={false}
            placeholder={t(tool === 'codex' ? 'settings.toolAccounts.keyPlaceholderCodex' : 'settings.toolAccounts.keyPlaceholderOpenCode')}
            value={key}
            onChange={(event) => setKey(event.target.value)}
            className="font-mono"
          />
        </div>
        <Button type="submit" variant="secondary" disabled={!ready || save.isPending}>
          {save.isPending ? <LoaderCircle className="animate-spin" /> : <KeyRound />}
          {t('common.save')}
        </Button>
      </div>
      {save.error && <Notice>{errorMessage(save.error)}</Notice>}
    </form>
  );
}
