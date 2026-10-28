import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, ChevronsUpDown, ExternalLink, Laptop, Link2, LoaderCircle, LogIn, LogOut, MonitorSmartphone, Server, ShieldCheck } from 'lucide-react';
import { Fragment, useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import type { EnvironmentTarget, HubAppPairing } from '../../preload';
import type { HubDevice, HubPairingInfo, RemoteStatus } from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { cn, errorMessage, timeAgo } from '../lib/utils';
import { QRCode } from './PhoneSettings';
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
  const t = useT();
  return <span className={cn('size-2 shrink-0 rounded-full', online ? 'bg-emerald-400 animate-glow' : 'bg-faint')} aria-label={online ? t('shell.env.online') : t('shell.env.offline')} />;
}

// EnvironmentSwitcher picks what the app manages: this machine, or an
// environment on a hub this app is connected to. Connecting is pairing: the
// app asks the hub, and a device already connected approves it; a hub's
// setup link connects at once. From here the app approves other devices, lists
// and revokes them, and connects this machine's AgentBox to a hub in one click.
export function EnvironmentSwitcher() {
  const t = useT();
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
  const [approving, setApproving] = useState<string | null>(null);
  const [listing, setListing] = useState<string | null>(null);
  const [connectingThis, setConnectingThis] = useState<string | null>(null);

  useEffect(() => window.agentbox.target.onChange((t) => queryClient.setQueryData(['target'], t)), [queryClient]);

  const select = useMutation({
    mutationFn: (next: EnvironmentTarget) => window.agentbox.target.set(next),
    onError: (err) => toast(t('shell.env.switchFailed'), { description: errorMessage(err) }),
  });
  const logout = useMutation({
    mutationFn: (url: string) => window.agentbox.hubs.logout(url),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['hubs'] }),
  });

  const current = target.data;
  const remote = current?.kind === 'hub';
  const hubIndex = remote ? (hubs.data ?? []).findIndex((h) => h.url === current.hub) : -1;
  const currentEnvironment = hubIndex >= 0 ? environments[hubIndex]?.data?.find((e) => e.id === current?.environmentId) : undefined;
  // This machine's own connection to a hub, which only the app managing it
  // can change.
  const local = !web && current?.kind === 'local';
  const remoteStatus = useQuery({ queryKey: ['remote-status'], queryFn: () => api.remote(), enabled: local && (hubs.data?.length ?? 0) > 0, refetchInterval: 10_000 });

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
              <span className="block truncate text-[13px] font-medium text-primary">{remote ? current.environmentName : t('shell.env.thisMachine')}</span>
              <span className="block truncate text-[11px] text-subtle">{remote ? hostOf(current.hub ?? '') : t('shell.env.local')}</span>
            </span>
            {remote && <OnlineDot online={currentEnvironment?.online} />}
            <ChevronsUpDown className="size-3.5 shrink-0 text-subtle" />
          </button>
        </MenuTrigger>
        <MenuContent align="start" className="w-[256px]">
          <MenuLabel>{t('shell.env.environments')}</MenuLabel>
          {!web && (
            <MenuItem icon={Laptop} data-environment-option="local" onSelect={() => select.mutate({ kind: 'local' })} hint={!remote ? <Check className="size-3.5 text-brand-300" /> : t('shell.env.local')}>
              {t('shell.env.thisMachine')}
            </MenuItem>
          )}
          {hubs.data?.map((hub, i) => (
            <Fragment key={hub.url}>
              {(!web || i > 0) && <MenuSeparator />}
              <MenuLabel>{hostOf(hub.url)}</MenuLabel>
              {environments[i]?.isPending && <div className="px-2.5 py-1.5 text-xs text-subtle">{t('common.loading')}</div>}
              {environments[i]?.error && <div className="px-2.5 py-1.5 text-xs text-rose-300">{errorMessage(environments[i].error)}</div>}
              {environments[i]?.data?.length === 0 && <div className="px-2.5 py-1.5 text-xs text-subtle">{t('shell.env.none')}</div>}
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
                      <span className="text-[11px] text-subtle">{env.online ? `${t('shell.env.online')}${env.hostname ? ` · ${env.hostname}` : ''}` : t('shell.env.offline')}</span>
                    </span>
                  </MenuItem>
                );
              })}
              {local && <ConnectThisItem hub={hub.url} status={remoteStatus.data} onSelect={() => setConnectingThis(hub.url)} />}
              <MenuItem icon={ShieldCheck} data-hub-approve onSelect={() => setApproving(hub.url)}>
                {t('shell.env.approve')}
              </MenuItem>
              <MenuItem icon={MonitorSmartphone} data-hub-devices onSelect={() => setListing(hub.url)}>
                {t('shell.env.devices')}
              </MenuItem>
              <MenuItem icon={LogOut} onSelect={() => logout.mutate(hub.url)} hint={hub.email}>
                {t('shell.env.signOut')}
              </MenuItem>
            </Fragment>
          ))}
          {!web && (
            <>
              <MenuSeparator />
              <MenuItem icon={LogIn} onSelect={() => setConnecting(true)}>
                {t('shell.env.connect')}
              </MenuItem>
            </>
          )}
        </MenuContent>
      </Menu>
      <ConnectHubDialog open={connecting} onOpenChange={setConnecting} />
      <ApproveDeviceDialog hub={approving} onClose={() => setApproving(null)} />
      <DevicesDialog hub={listing} onClose={() => setListing(null)} />
      <ConnectThisDialog hub={connectingThis} onClose={() => setConnectingThis(null)} />
    </>
  );
}

// ConnectThisItem connects the AgentBox this app manages to a hub, or says
// that it is.
function ConnectThisItem({ hub, status, onSelect }: { hub: string; status?: RemoteStatus; onSelect: () => void }) {
  const t = useT();
  if (status?.configured && status.hub === hub) {
    return (
      <MenuItem icon={Laptop} disabled data-hub-connected-this hint={<OnlineDot online={status.connected} />}>
        {t('shell.env.thisConnected')}
      </MenuItem>
    );
  }
  return (
    <MenuItem icon={Link2} data-hub-connect-this onSelect={onSelect}>
      {t('shell.env.connectThis')}
    </MenuItem>
  );
}

function ConnectHubDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [input, setInput] = useState('https://');
  const [pairing, setPairing] = useState<HubAppPairing | null>(null);
  // passwordHub is the hub a password signs in to: one from before pairing,
  // or one with such accounts, when the user chose a password.
  const [passwordHub, setPasswordHub] = useState<string | null>(null);
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string>();

  const connected = async (account: { url: string; email: string }) => {
    await queryClient.invalidateQueries({ queryKey: ['hubs'] });
    toast(t('shell.env.signedIn', { host: hostOf(account.url) }), account.email ? { description: account.email } : undefined);
    onOpenChange(false);
  };
  const connect = useMutation({
    mutationFn: () => window.agentbox.hubs.connect(input.trim()),
    onMutate: () => setError(undefined),
    onSuccess: async (result) => {
      if (result.account) return connected(result.account);
      if (result.passwordOnly) return setPasswordHub(result.passwordOnly);
      if (!result.pairing) return;
      const p = result.pairing;
      setPairing(p);
      void window.agentbox.openExternal(p.approveURL);
      try {
        await connected(await window.agentbox.hubs.pairWait(p.id));
      } catch (err) {
        // Closing the dialog cancels it, and says nothing.
        setPairing((now) => {
          if (now?.id === p.id) setError(errorMessage(err));
          return now?.id === p.id ? null : now;
        });
      }
    },
    onError: (err) => setError(errorMessage(err)),
  });
  const login = useMutation({
    mutationFn: () => window.agentbox.hubs.login(passwordHub ?? '', email.trim(), password),
    onSuccess: (account) => {
      if (pairing) void window.agentbox.hubs.pairCancel(pairing.id);
      setPassword('');
      return connected(account);
    },
    onError: (err) => setError(errorMessage(err)),
  });

  useEffect(() => {
    if (open) return;
    setPairing((p) => {
      if (p) void window.agentbox.hubs.pairCancel(p.id);
      return null;
    });
    setPasswordHub(null);
    setPassword('');
    setError(undefined);
    connect.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('shell.env.connectTitle')}</DialogTitle>
          <DialogDescription>{pairing && !passwordHub ? t('shell.env.pairBody', { host: hostOf(pairing.url) }) : t('shell.env.connectBody')}</DialogDescription>
        </DialogHeader>
        {!pairing && !passwordHub ? (
          <form
            className="grid gap-4"
            onSubmit={(event) => {
              event.preventDefault();
              connect.mutate();
            }}
          >
            <Field label={t('shell.env.hubAddress')} htmlFor="hub-url" hint={t('shell.env.hubAddressHint')}>
              <Input id="hub-url" autoFocus className="font-mono text-[13px]" placeholder="https://hub.example.com" value={input} onChange={(event) => setInput(event.target.value)} />
            </Field>
            {error && <Notice>{error}</Notice>}
            <DialogFooter>
              <Button variant="ghost" onClick={() => onOpenChange(false)}>
                {t('common.cancel')}
              </Button>
              <Button type="submit" variant="primary" disabled={!/^(https?:\/\/)?[^/\s]+\S*$/.test(input.trim()) || input.trim() === 'https://' || connect.isPending}>
                {connect.isPending && <LoaderCircle className="animate-spin" />}
                {t('shell.env.connectButton')}
              </Button>
            </DialogFooter>
          </form>
        ) : pairing && !passwordHub ? (
          <div className="grid gap-4" data-hub-pairing>
            <div className="flex flex-col items-center gap-4 rounded-2xl border border-line bg-surface-faint p-5 sm:flex-row sm:items-start">
              {pairing.qr.length > 0 && <QRCode rows={pairing.qr} size={148} />}
              <div className="grid min-w-0 gap-2">
                <div className="text-[12px] text-subtle">{t('shell.env.code')}</div>
                <div className="font-mono text-2xl font-semibold tracking-[0.08em] text-title" data-hub-pairing-code>
                  {pairing.code}
                </div>
                <p className="text-[13px] text-tertiary">{t('shell.env.pairHow')}</p>
                <div className="flex items-center gap-2 text-[12px] text-subtle">
                  <LoaderCircle className="size-3.5 animate-spin" />
                  {t('shell.env.waiting')}
                </div>
              </div>
            </div>
            {error && <Notice>{error}</Notice>}
            <DialogFooter>
              {pairing.passwords && (
                <Button variant="ghost" className="mr-auto" onClick={() => setPasswordHub(pairing.url)}>
                  {t('shell.env.usePassword')}
                </Button>
              )}
              <Button variant="ghost" onClick={() => onOpenChange(false)}>
                {t('common.cancel')}
              </Button>
              <Button onClick={() => void window.agentbox.openExternal(pairing.approveURL)}>
                <ExternalLink />
                {t('shell.env.openApproval')}
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <form
            className="grid gap-4"
            onSubmit={(event) => {
              event.preventDefault();
              login.mutate();
            }}
          >
            <p className="text-[13px] text-tertiary">{t('shell.env.passwordBody')}</p>
            <Field label={t('shell.env.email')} htmlFor="hub-email">
              <Input id="hub-email" type="email" autoFocus autoComplete="username" value={email} onChange={(event) => setEmail(event.target.value)} />
            </Field>
            <Field label={t('shell.env.password')} htmlFor="hub-password">
              <Input id="hub-password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} />
            </Field>
            {error && <Notice>{error}</Notice>}
            <DialogFooter>
              <Button variant="ghost" onClick={() => setPasswordHub(null)}>
                {t('common.back')}
              </Button>
              <Button type="submit" variant="primary" disabled={!email.trim() || !password || login.isPending}>
                {login.isPending && <LoaderCircle className="animate-spin" />}
                {t('shell.env.signIn')}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

// PairingDetails says what a pairing would connect, for whoever approves it.
function PairingDetails({ info }: { info: HubPairingInfo }) {
  const t = useT();
  const environment = info.kind === 'environment';
  const rows: [string, string][] = [
    [t('shell.env.code'), info.code],
    [t('shell.env.what'), environment ? t('shell.env.whatEnvironment', { name: info.name ?? '' }) : t('shell.env.whatDevice', { label: info.label })],
  ];
  if (info.hostname) rows.push([t('shell.env.machine'), info.hostname]);
  if (info.version) rows.push([t('shell.env.agentboxVersion'), info.version]);
  if (info.address) rows.push([t('shell.env.from'), info.address]);
  return (
    <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1.5 rounded-xl border border-line bg-surface-faint px-4 py-3 text-[13px]" data-hub-pairing-info>
      {rows.map(([k, v]) => (
        <Fragment key={k}>
          <dt className="text-subtle">{k}</dt>
          <dd className="min-w-0 break-words text-primary">{v}</dd>
        </Fragment>
      ))}
    </dl>
  );
}

// ApproveDeviceDialog approves a device or an environment asking to connect
// to a hub, by the code it shows.
function ApproveDeviceDialog({ hub, onClose }: { hub: string | null; onClose: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [code, setCode] = useState('');
  const lookup = useMutation({ mutationFn: () => window.agentbox.hubs.pairing(hub ?? '', code.trim()) });
  const decide = useMutation({
    mutationFn: (approve: boolean) => window.agentbox.hubs.decide(hub ?? '', lookup.data?.code ?? '', approve),
    onSuccess: (info, approve) => {
      void queryClient.invalidateQueries({ queryKey: ['hub-environments', hub] });
      toast(approve ? t('shell.env.approved', { what: info.kind === 'environment' ? (info.name ?? '') : info.label }) : t('shell.env.denied'));
      onClose();
    },
  });

  useEffect(() => {
    if (hub === null) return;
    setCode('');
    lookup.reset();
    decide.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hub]);

  const error = lookup.error ?? decide.error;
  return (
    <Dialog open={hub !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('shell.env.approveTitle')}</DialogTitle>
          <DialogDescription>{lookup.data ? t('shell.env.approveCheck') : t('shell.env.approveBody', { host: hostOf(hub ?? '') })}</DialogDescription>
        </DialogHeader>
        {!lookup.data ? (
          <form
            className="grid gap-4"
            onSubmit={(event) => {
              event.preventDefault();
              lookup.mutate();
            }}
          >
            <Field label={t('shell.env.code')} htmlFor="pairing-code">
              <Input
                id="pairing-code"
                autoFocus
                autoComplete="off"
                spellCheck={false}
                className="font-mono text-[15px] uppercase tracking-[0.08em]"
                placeholder="BCDF-GHJK"
                value={code}
                onChange={(event) => setCode(event.target.value)}
              />
            </Field>
            {error && <Notice>{errorMessage(error)}</Notice>}
            <DialogFooter>
              <Button variant="ghost" onClick={onClose}>
                {t('common.cancel')}
              </Button>
              <Button type="submit" variant="primary" disabled={code.replace(/[^a-z]/gi, '').length !== 8 || lookup.isPending}>
                {lookup.isPending && <LoaderCircle className="animate-spin" />}
                {t('shell.env.continue')}
              </Button>
            </DialogFooter>
          </form>
        ) : (
          <div className="grid gap-4">
            <PairingDetails info={lookup.data} />
            <Notice tone="warning">{t('shell.env.approveWarning')}</Notice>
            {error && <Notice>{errorMessage(error)}</Notice>}
            <DialogFooter>
              <Button variant="ghost" disabled={decide.isPending} onClick={() => decide.mutate(false)}>
                {t('shell.env.deny')}
              </Button>
              <Button variant="primary" disabled={decide.isPending} onClick={() => decide.mutate(true)} data-hub-approve-button>
                {decide.isPending && <LoaderCircle className="animate-spin" />}
                {t('shell.env.approveButton')}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

// DevicesDialog lists what's signed in to a hub, and revokes it.
function DevicesDialog({ hub, onClose }: { hub: string | null; onClose: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const devices = useQuery({ queryKey: ['hub-devices', hub], queryFn: () => window.agentbox.hubs.devices(hub ?? ''), enabled: hub !== null });
  const [confirming, setConfirming] = useState<string | null>(null);
  const revoke = useMutation({
    mutationFn: (d: HubDevice) => window.agentbox.hubs.revokeDevice(hub ?? '', d.id),
    onSuccess: (_, d) => {
      toast(t('shell.env.revoked', { label: d.label }));
      setConfirming(null);
      void queryClient.invalidateQueries({ queryKey: ['hub-devices', hub] });
    },
    onError: (err) => toast.error(t('shell.env.revokeFailed'), { description: errorMessage(err) }),
  });

  useEffect(() => setConfirming(null), [hub]);

  return (
    <Dialog open={hub !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('shell.env.devicesTitle')}</DialogTitle>
          <DialogDescription>{t('shell.env.devicesBody', { host: hostOf(hub ?? '') })}</DialogDescription>
        </DialogHeader>
        {devices.error && <Notice>{errorMessage(devices.error)}</Notice>}
        {devices.isPending && <div className="text-[13px] text-subtle">{t('common.loading')}</div>}
        <ul className="grid divide-y divide-line rounded-xl border border-line" data-hub-device-list>
          {devices.data?.map((d) => (
            <li key={d.id} className="flex items-center gap-3 px-3.5 py-2.5" data-hub-device={d.label}>
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2 text-[13px] text-primary">
                  <span className="truncate">{d.label}</span>
                  {d.current && <span className="shrink-0 rounded-full border border-brand-400/40 px-1.5 text-[10px] text-brand-300">{t('shell.env.thisApp')}</span>}
                </div>
                <div className="truncate text-[11px] text-subtle">
                  {t('shell.env.deviceSeen', { connected: timeAgo(d.createdAt), seen: d.lastSeenAt ? timeAgo(d.lastSeenAt) : t('shell.env.never') })}
                </div>
              </div>
              {!d.current && (
                <Button
                  size="sm"
                  variant={confirming === d.id ? 'destructive' : 'danger'}
                  disabled={revoke.isPending}
                  onClick={() => (confirming === d.id ? revoke.mutate(d) : setConfirming(d.id))}
                  data-hub-revoke={d.label}
                >
                  {confirming === d.id ? t('shell.env.revokeConfirm') : t('shell.env.revoke')}
                </Button>
              )}
            </li>
          ))}
        </ul>
        <DialogFooter>
          <Button variant="primary" onClick={onClose}>
            {t('common.done')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ConnectThisDialog connects the AgentBox this app manages to a hub as an
// environment, in one click: its daemon asks the hub, and this app, connected
// to the hub, approves the code it was given, as any device would.
function ConnectThisDialog({ hub, onClose }: { hub: string | null; onClose: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [step, setStep] = useState<'asking' | 'approving' | 'connecting' | 'done'>('asking');
  const [code, setCode] = useState('');
  const [name, setName] = useState('');
  const run = useRef(0);
  const connect = useMutation({
    mutationFn: async (url: string) => {
      const id = ++run.current;
      setStep('asking');
      const st = await api.pairRemote({ hub: url });
      if (!st.pairing) throw new Error(t('shell.env.connectThisFailed'));
      setCode(st.pairing.code);
      setStep('approving');
      const info = await window.agentbox.hubs.decide(url, st.pairing.code, true);
      setName(info.name ?? '');
      setStep('connecting');
      for (let i = 0; i < 60 && run.current === id; i++) {
        await new Promise((resolve) => setTimeout(resolve, 500));
        const now = await api.remote();
        if (now.pairing?.error) throw new Error(now.pairing.error);
        if (!now.pairing && now.connected && now.hub === url) {
          setStep('done');
          return;
        }
      }
      throw new Error(t('shell.env.connectThisSlow'));
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ['remote-status'] });
      void queryClient.invalidateQueries({ queryKey: ['hub-environments', hub] });
    },
  });

  useEffect(() => {
    if (hub === null) {
      run.current++;
      return;
    }
    connect.reset();
    setCode('');
    setName('');
    connect.mutate(hub);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hub]);

  const steps: { key: typeof step; label: string }[] = [
    { key: 'asking', label: t('shell.env.stepAsking', { host: hostOf(hub ?? '') }) },
    { key: 'approving', label: code ? t('shell.env.stepApprovingCode', { code }) : t('shell.env.stepApproving') },
    { key: 'connecting', label: t('shell.env.stepConnecting') },
  ];
  const order = ['asking', 'approving', 'connecting', 'done'];
  return (
    <Dialog open={hub !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('shell.env.connectThisTitle')}</DialogTitle>
          <DialogDescription>{t('shell.env.connectThisBody', { host: hostOf(hub ?? '') })}</DialogDescription>
        </DialogHeader>
        <ol className="grid gap-2 text-[13px]" data-connect-this-steps>
          {steps.map((s) => {
            const done = order.indexOf(step) > order.indexOf(s.key);
            const active = step === s.key && !connect.error;
            return (
              <li key={s.key} className={cn('flex items-center gap-2.5', done ? 'text-primary' : active ? 'text-secondary' : 'text-faint')}>
                {done ? <Check className="size-4 text-emerald-400" /> : active ? <LoaderCircle className="size-4 animate-spin" /> : <span className="size-4" />}
                {s.label}
              </li>
            );
          })}
        </ol>
        {step === 'done' && <Notice tone="info">{t('shell.env.connectThisDone', { name: name || hostOf(hub ?? ''), host: hostOf(hub ?? '') })}</Notice>}
        {connect.error && <Notice>{errorMessage(connect.error)}</Notice>}
        <DialogFooter>
          {connect.error && hub && (
            <Button variant="ghost" onClick={() => connect.mutate(hub)}>
              {t('shell.env.tryAgain')}
            </Button>
          )}
          <Button variant={step === 'done' ? 'primary' : 'ghost'} onClick={onClose} data-connect-this-close>
            {step === 'done' ? t('common.done') : t('common.cancel')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
