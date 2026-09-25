import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, ChevronsUpDown, Copy, Laptop, LoaderCircle, LogIn, LogOut, Plus, Server } from 'lucide-react';
import { Fragment, useEffect, useState } from 'react';
import { toast } from 'sonner';
import type { EnvironmentTarget } from '../../preload';
import { cn, errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input } from './ui/input';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from './ui/menu';

function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

function OnlineDot({ online }: { online?: boolean }) {
  return <span className={cn('size-2 shrink-0 rounded-full', online ? 'bg-emerald-400 animate-glow' : 'bg-faint')} aria-label={online ? 'online' : 'offline'} />;
}

// EnvironmentSwitcher picks what the app manages: this machine, or an
// environment on a hub you signed in to.
export function EnvironmentSwitcher() {
  // In a browser the hub serving the page is the only hub, and there's no machine of your own to manage.
  const web = 'web' in window.agentbox;
  const queryClient = useQueryClient();
  const target = useQuery({ queryKey: ['target'], queryFn: () => window.agentbox.target.get(), staleTime: Infinity });
  const hubs = useQuery({ queryKey: ['hubs'], queryFn: () => window.agentbox.hubs.list(), staleTime: Infinity });
  const environments = useQueries({
    queries: (hubs.data ?? []).map((hub) => ({
      queryKey: ['hub-environments', hub.url],
      queryFn: () => window.agentbox.hubs.environments(hub.url),
      refetchInterval: 10_000,
    })),
  });
  const [connecting, setConnecting] = useState(false);
  const [adding, setAdding] = useState<string | null>(null);

  useEffect(() => window.agentbox.target.onChange((t) => queryClient.setQueryData(['target'], t)), [queryClient]);

  const select = useMutation({
    mutationFn: (next: EnvironmentTarget) => window.agentbox.target.set(next),
    onError: (err) => toast('Couldn’t switch environments', { description: errorMessage(err) }),
  });
  const logout = useMutation({
    mutationFn: (url: string) => window.agentbox.hubs.logout(url),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['hubs'] }),
  });

  const current = target.data;
  const remote = current?.kind === 'hub';
  const hubIndex = remote ? (hubs.data ?? []).findIndex((h) => h.url === current.hub) : -1;
  const currentEnvironment = hubIndex >= 0 ? environments[hubIndex]?.data?.find((e) => e.id === current?.environmentId) : undefined;

  return (
    <>
      <Menu>
        <MenuTrigger asChild>
          <button
            data-environment-switcher={remote ? current.environmentName : 'local'}
            className="flex w-full items-center gap-2.5 rounded-xl border border-line bg-surface-faint px-2.5 py-2 text-left transition hover:border-line-vivid hover:bg-surface"
          >
            <span className="flex size-7 shrink-0 items-center justify-center rounded-lg bg-surface text-tertiary ring-1 ring-inset ring-line">
              {remote ? <Server className="size-3.5" /> : <Laptop className="size-3.5" />}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[13px] font-medium text-primary">{remote ? current.environmentName : 'This machine'}</span>
              <span className="block truncate text-[11px] text-subtle">{remote ? hostOf(current.hub ?? '') : 'Local'}</span>
            </span>
            {remote && <OnlineDot online={currentEnvironment?.online} />}
            <ChevronsUpDown className="size-3.5 shrink-0 text-subtle" />
          </button>
        </MenuTrigger>
        <MenuContent align="start" className="w-[256px]">
          <MenuLabel>Environments</MenuLabel>
          {!web && (
            <MenuItem icon={Laptop} data-environment-option="local" onSelect={() => select.mutate({ kind: 'local' })} hint={!remote ? <Check className="size-3.5 text-brand-300" /> : 'Local'}>
              This machine
            </MenuItem>
          )}
          {hubs.data?.map((hub, i) => (
            <Fragment key={hub.url}>
              {(!web || i > 0) && <MenuSeparator />}
              <MenuLabel>{hostOf(hub.url)}</MenuLabel>
              {environments[i]?.isPending && <div className="px-2.5 py-1.5 text-xs text-subtle">Loading…</div>}
              {environments[i]?.error && <div className="px-2.5 py-1.5 text-xs text-rose-300">{errorMessage(environments[i].error)}</div>}
              {environments[i]?.data?.length === 0 && <div className="px-2.5 py-1.5 text-xs text-subtle">No environments yet</div>}
              {environments[i]?.data?.map((env) => {
                const selected = remote && current.hub === hub.url && current.environmentId === env.id;
                return (
                  <MenuItem
                    key={env.id}
                    icon={Server}
                    data-environment-option={env.name}
                    data-online={env.online}
                    onSelect={() => select.mutate({ kind: 'hub', hub: hub.url, environmentId: env.id, environmentName: env.name })}
                    hint={selected ? <Check className="size-3.5 text-brand-300" /> : <OnlineDot online={env.online} />}
                  >
                    <span className="flex flex-col">
                      <span>{env.name}</span>
                      <span className="text-[11px] text-subtle">{env.online ? `online${env.hostname ? ` · ${env.hostname}` : ''}` : 'offline'}</span>
                    </span>
                  </MenuItem>
                );
              })}
              <MenuItem icon={Plus} onSelect={() => setAdding(hub.url)}>
                Add an environment…
              </MenuItem>
              <MenuItem icon={LogOut} onSelect={() => logout.mutate(hub.url)} hint={hub.email}>
                Sign out
              </MenuItem>
            </Fragment>
          ))}
          {!web && (
            <>
              <MenuSeparator />
              <MenuItem icon={LogIn} onSelect={() => setConnecting(true)}>
                Connect to a hub…
              </MenuItem>
            </>
          )}
        </MenuContent>
      </Menu>
      <ConnectHubDialog open={connecting} onOpenChange={setConnecting} />
      <AddEnvironmentDialog hub={adding} onClose={() => setAdding(null)} />
    </>
  );
}

function ConnectHubDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const queryClient = useQueryClient();
  const [url, setUrl] = useState('https://');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const login = useMutation({
    mutationFn: () => window.agentbox.hubs.login(url.trim(), email.trim(), password),
    onSuccess: async (account) => {
      setPassword('');
      await queryClient.invalidateQueries({ queryKey: ['hubs'] });
      toast(`Signed in to ${hostOf(account.url)}`, { description: account.email });
      onOpenChange(false);
    },
  });

  useEffect(() => {
    if (!open) return;
    setPassword('');
    login.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Connect to a hub</DialogTitle>
          <DialogDescription>
            A hub puts your environments in one place: a VPS, another PC, this one. Sign in with the account its owner created on it.
          </DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            login.mutate();
          }}
        >
          <Field label="Hub address" htmlFor="hub-url">
            <Input id="hub-url" autoFocus className="font-mono text-[13px]" placeholder="https://hub.example.com" value={url} onChange={(event) => setUrl(event.target.value)} />
          </Field>
          <Field label="Email" htmlFor="hub-email">
            <Input id="hub-email" type="email" autoComplete="username" value={email} onChange={(event) => setEmail(event.target.value)} />
          </Field>
          <Field label="Password" htmlFor="hub-password">
            <Input id="hub-password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} />
          </Field>
          {login.error && <Notice>{errorMessage(login.error)}</Notice>}
          <DialogFooter>
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={!/^https?:\/\/.+/.test(url.trim()) || !email.trim() || !password || login.isPending}>
              {login.isPending && <LoaderCircle className="animate-spin" />}
              Sign in
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function AddEnvironmentDialog({ hub, onClose }: { hub: string | null; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState('');
  const [copied, setCopied] = useState(false);
  const add = useMutation({
    mutationFn: () => window.agentbox.hubs.addEnvironment(hub ?? '', name.trim()),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['hub-environments', hub] }),
  });

  useEffect(() => {
    if (hub === null) return;
    setName('');
    setCopied(false);
    add.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hub]);

  const command = add.data ? `agentbox remote connect ${hub} --token ${add.data.token}` : '';
  return (
    <Dialog open={hub !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add an environment</DialogTitle>
          <DialogDescription>
            An environment is a machine running AgentBox, like a VPS. Name it, then run the command this gives you on that machine: it connects out to the hub, so
            nothing on it has to be reachable.
          </DialogDescription>
        </DialogHeader>
        {!add.data ? (
          <form
            className="grid gap-4"
            onSubmit={(event) => {
              event.preventDefault();
              add.mutate();
            }}
          >
            <Field label="Name" htmlFor="environment-name" hint="Lowercase letters, digits and dashes, like vps or home-pc.">
              <Input id="environment-name" autoFocus className="font-mono text-[13px]" placeholder="vps" value={name} onChange={(event) => setName(event.target.value)} />
            </Field>
            {add.error && <Notice>{errorMessage(add.error)}</Notice>}
            <DialogFooter>
              <Button variant="ghost" onClick={onClose}>
                Cancel
              </Button>
              <Button type="submit" variant="primary" disabled={!name.trim() || add.isPending}>
                {add.isPending && <LoaderCircle className="animate-spin" />}
                Add environment
              </Button>
            </DialogFooter>
          </form>
        ) : (
          <div className="grid gap-3">
            <p className="text-[13px] text-tertiary">
              On <span className="font-medium text-primary">{add.data.environment.name}</span>’s machine, run:
            </p>
            <div className="flex items-start gap-2 rounded-xl border border-line-strong bg-well py-2 pl-3.5 pr-1.5 font-mono text-[12px] leading-relaxed text-secondary">
              <span className="select-none text-faint">$</span>
              <span className="min-w-0 flex-1 break-all" data-connect-command>
                {command}
              </span>
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label="Copy the command"
                onClick={() => {
                  window.agentbox.copyText(command);
                  setCopied(true);
                }}
              >
                {copied ? <Check className="text-emerald-400" /> : <Copy />}
              </Button>
            </div>
            <Notice tone="warning">The token isn’t shown again, and it lets a machine connect as {add.data.environment.name}: keep it private.</Notice>
            <DialogFooter>
              <Button variant="primary" onClick={onClose}>
                Done
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
