import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Globe, Smartphone } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatDate, formatDateTime, t, useT, type MessageKey } from '../lib/i18n';
import type { SettingGroup } from '../lib/settingsSearch';
import { cn, errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { Button } from './ui/button';
import { Field, Input } from './ui/input';
import { SettingNote, SettingRow } from './ui/settings';
import { Switch } from './ui/switch';

// Chatting from a phone on the local network (internal/daemon/lan.go): the
// daemon serves the app's web version to phones paired here with a QR code.
// Reach from anywhere puts the same page on the internet through a Cloudflare
// Tunnel the daemon runs (internal/daemon/lantunnel.go).

export function phoneGroups(): SettingGroup[] {
  return [
    {
      id: 'phone',
      title: t('defaults.phone.title'),
      entries: [
        {
          id: 'phone-chat',
          label: t('defaults.phone.chatLabel'),
          keywords: t('defaults.phone.chatKeywords'),
          render: () => <PhoneChat />,
        },
        {
          id: 'phone-tunnel',
          label: t('defaults.phone.tunnelLabel'),
          keywords: t('defaults.phone.tunnelKeywords'),
          render: () => <PhoneTunnel />,
        },
        {
          id: 'phones',
          label: t('defaults.phone.pairedLabel'),
          keywords: t('defaults.phone.pairedKeywords'),
          render: () => <PairedPhones />,
        },
      ],
    },
  ];
}

function useLAN() {
  return useQuery({ queryKey: ['lan'], queryFn: api.lan });
}

function PhoneChat() {
  const t = useT();
  const lan = useLAN();
  const queryClient = useQueryClient();
  const [pairing, setPairing] = useState<T.LANPairing | null>(null);
  const pair = useMutation({
    mutationFn: api.pairLAN,
    onSuccess: setPairing,
    onError: (err) => toast.error(t('defaults.phone.qrFailed'), { description: errorMessage(err) }),
  });
  const save = useMutation({
    mutationFn: (enabled: boolean) => api.updateLAN({ enabled }),
    onSuccess: (next) => {
      queryClient.setQueryData(['lan'], next);
      if (!next.enabled) setPairing(null);
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const st = lan.data;
  // Serving plain HTTP on the network is asked first: pairing a phone is
  // what the button is for, and that is what it takes.
  const [confirming, setConfirming] = useState(false);
  // Turned on, the QR code is shown as soon as there's an address to put in it.
  const wantQR = useRef(false);
  useEffect(() => {
    if (wantQR.current && st?.enabled && (st.listening || st.tunnel.url) && st.urls.length > 0 && !pair.isPending) {
      wantQR.current = false;
      pair.mutate();
    }
  }, [st, pair]);
  // When the tunnel comes up, or its address changes, the QR code is made
  // again for its address, which works from anywhere.
  const tunnelURL = st?.enabled ? st.tunnel.url : undefined;
  const shownFor = useRef(tunnelURL);
  useEffect(() => {
    if (tunnelURL === shownFor.current) return;
    shownFor.current = tunnelURL;
    if (tunnelURL && !pair.isPending) pair.mutate();
  }, [tunnelURL, pair]);
  // The QR code goes once a phone pairs with it.
  const phones = st?.phones.length ?? 0;
  const before = useRef(phones);
  useEffect(() => {
    if (phones > before.current && pairing) {
      setPairing(null);
      toast.success(st?.phones[0]?.name ? t('defaults.phone.pairedToast', { name: st.phones[0].name }) : t('defaults.phone.pairedToastYourPhone'));
    }
    before.current = phones;
  }, [phones, pairing, st, t]);

  return (
    <SettingRow
      label={t('defaults.phone.chatLabel')}
      description={t('defaults.phone.chatDescription')}
      details={t('defaults.phone.chatDetails', { port: st?.port ?? 7780 })}
      control={
        st?.enabled ? (
          <Switch
            data-phone-chat
            aria-label={t('defaults.phone.chatLabel')}
            disabled={save.isPending}
            checked
            onCheckedChange={() => save.mutate(false)}
          />
        ) : (
          <Button size="sm" variant="primary" data-phone-start disabled={save.isPending || !st} onClick={() => setConfirming(true)}>
            <Smartphone className="size-3.5" /> {t('defaults.phone.pair')}
          </Button>
        )
      }
    >
      <ServeConfirm
        open={confirming}
        onOpenChange={setConfirming}
        port={st?.port ?? 7780}
        onConfirm={() => {
          wantQR.current = true;
          return save.mutateAsync(true);
        }}
      />
      {st?.enabled && (
        <div className="grid gap-3">
          <SettingNote tone="warning">{t('defaults.phone.plainHttp')}</SettingNote>
          {!st.tunnel.enabled && <SettingNote>{t('defaults.phone.httpsOnly')}</SettingNote>}
          {st.tunnel.url && !st.listening ? null : st.listening && st.urls.length > 0 ? (
            <SettingNote>
              {st.urls.length > 1
                ? t.rich('defaults.phone.phonesOpenMore', { url: (c) => <span className="break-all font-mono text-primary">{c}</span>, address: st.urls[0], others: st.urls.slice(1).join(', ') })
                : t.rich('defaults.phone.phonesOpen', { url: (c) => <span className="break-all font-mono text-primary">{c}</span>, address: st.urls[0] })}
            </SettingNote>
          ) : (
            <SettingNote tone={st.error?.startsWith('waiting') ? 'muted' : 'error'}>{st.error || t('defaults.phone.openingPort')}</SettingNote>
          )}
          {!st.webVersion && <SettingNote>{t('defaults.phone.gettingPage')}</SettingNote>}
          {pairing ? (
            <PairingCode pairing={pairing} onAgain={() => pair.mutate()} onDone={() => setPairing(null)} />
          ) : (
            (st.listening || st.tunnel.url) &&
            st.urls.length > 0 && (
              <div>
                <Button size="sm" variant="secondary" data-phone-pair disabled={pair.isPending} onClick={() => pair.mutate()}>
                  <Smartphone className="size-3.5" /> {t('defaults.phone.pair')}
                </Button>
              </div>
            )
          )}
        </div>
      )}
    </SettingRow>
  );
}

// ServeConfirm asks before AgentBox serves its phone page over plain HTTP on
// this computer's network, which both switches here turn on.
function ServeConfirm({ open, onOpenChange, port, onConfirm }: { open: boolean; onOpenChange: (open: boolean) => void; port: number; onConfirm: () => Promise<unknown> }) {
  const t = useT();
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('defaults.phone.serveTitle')}
      description={t('defaults.phone.serveDescription', { port })}
      confirmLabel={t('defaults.phone.serveConfirm')}
      onConfirm={onConfirm}
    />
  );
}

// The tunnels phones can reach this computer through, as the daemon names
// them: the hub's stable address first, the default when connected to a hub.
const tunnelModes = ['hub', 'quick', 'named'] as const;

function PhoneTunnel() {
  const t = useT();
  const lan = useLAN();
  const queryClient = useQueryClient();
  const st = lan.data;
  const tunnel = st?.tunnel;
  const [editing, setEditing] = useState(false);
  const [hostname, setHostname] = useState('');
  const [token, setToken] = useState('');
  const save = useMutation({
    mutationFn: (req: T.UpdateLANRequest) => api.updateLAN(req),
    onSuccess: (next) => {
      queryClient.setQueryData(['lan'], next);
      setEditing(false);
      setToken('');
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const on = !!tunnel?.enabled && !!st?.enabled;
  const [confirming, setConfirming] = useState(false);
  return (
    <SettingRow
      label={t('defaults.phone.tunnelLabel')}
      description={t('defaults.phone.tunnelDescription')}
      details={t('defaults.phone.tunnelDetails')}
      control={
        <Switch
          data-phone-tunnel
          aria-label={t('defaults.phone.tunnelLabel')}
          disabled={save.isPending || !st}
          checked={on}
          // Turning it on turns chatting from a phone on too: the tunnel only runs with it.
          onCheckedChange={(next) => {
            if (next && !st?.enabled) setConfirming(true);
            else save.mutate(next ? { enabled: true, tunnel: true } : { tunnel: false });
          }}
        />
      }
    >
      <ServeConfirm
        open={confirming}
        onOpenChange={setConfirming}
        port={st?.port ?? 7780}
        onConfirm={() => save.mutateAsync({ enabled: true, tunnel: true })}
      />
      {tunnel && (on || editing) && (
        <div className="grid gap-3">
          {on && (
            <SettingNote tone="warning">
              {t('defaults.phone.tunnelWarning')}
            </SettingNote>
          )}
          {on &&
            (tunnel.state === 'running' && tunnel.url ? (
              <SettingNote>
                <Globe className="mr-1 inline size-3.5 align-[-2px]" />
                {t.rich(tunnel.mode === 'quick' ? 'defaults.phone.tunnelOpenQuick' : 'defaults.phone.tunnelOpenNamed', {
                  url: (c) => <span className="break-all font-mono text-primary">{c}</span>,
                  address: tunnel.url,
                })}
              </SettingNote>
            ) : (
              <SettingNote tone={tunnel.state === 'failed' ? 'error' : 'muted'}>{tunnel.error || t('defaults.phone.startingTunnel')}</SettingNote>
            ))}
          {on && !editing && (
            <div className="grid gap-2" role="radiogroup" aria-label={t('defaults.phone.tunnelAddress')} data-tunnel-mode-choice>
              {tunnelModes.map((m) => {
                const noHub = m === 'hub' && (!tunnel.hub || !!tunnel.hubHasNoAddresses);
                return (
                  <button
                    key={m}
                    type="button"
                    role="radio"
                    aria-checked={tunnel.mode === m}
                    data-tunnel-mode={m}
                    disabled={save.isPending || noHub}
                    onClick={() => {
                      if (tunnel.mode === m) return;
                      if (m === 'named' && !tunnel.namedHostname) {
                        setHostname('');
                        setEditing(true);
                        return;
                      }
                      save.mutate({ tunnelMode: m });
                    }}
                    className={cn(
                      'grid gap-0.5 rounded-xl border px-4 py-3 text-left transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50 disabled:cursor-not-allowed disabled:opacity-60',
                      tunnel.mode === m ? 'border-brand-500/60 bg-brand-500/10' : 'border-line bg-surface-faint enabled:hover:bg-surface-raised',
                    )}
                  >
                    <span className="text-[13px] font-medium text-title">{t(`defaults.phone.mode.${m}`)}</span>
                    <span className="min-w-0 break-words text-[12px] text-muted">
                      {m === 'hub'
                        ? !tunnel.hub
                          ? t('defaults.phone.mode.hub.noHub')
                          : tunnel.hubHasNoAddresses
                            ? t('defaults.phone.mode.hub.none', { hub: tunnel.hub })
                            : t('defaults.phone.mode.hub.hint', { hub: tunnel.hub })
                        : m === 'named' && tunnel.namedHostname
                          ? t('defaults.phone.mode.named.set', { hostname: tunnel.namedHostname })
                          : t(`defaults.phone.mode.${m}.hint`)}
                    </span>
                  </button>
                );
              })}
            </div>
          )}
          {tunnel.named && !editing && (
            <SettingNote>
              {t.rich('defaults.phone.namedTunnel', {
                mono: (c) => <span className="break-all font-mono text-primary">{c}</span>,
                hostname: tunnel.hostname,
                origin: tunnel.origin,
              })}
            </SettingNote>
          )}
          {editing ? (
            <form
              className="grid gap-3 rounded-xl border border-line p-3"
              onSubmit={(e) => {
                e.preventDefault();
                save.mutate({ tunnelHostname: hostname, ...(token || !tunnel.namedHostname ? { tunnelToken: token } : {}) });
              }}
            >
              <SettingNote>
                {t.rich('defaults.phone.makeTunnel', { mono: (c) => <span className="break-all font-mono text-primary">{c}</span>, origin: tunnel.origin })}
              </SettingNote>
              <Field label={t('defaults.phone.publicHostname')} htmlFor="tunnel-hostname">
                <Input id="tunnel-hostname" placeholder="chat.example.com" value={hostname} onChange={(e) => setHostname(e.target.value)} />
              </Field>
              <Field label={t('defaults.phone.token')} htmlFor="tunnel-token" hint={tunnel.namedHostname ? t('defaults.phone.tokenKeep') : t('defaults.phone.tokenNew')}>
                <Input
                  id="tunnel-token"
                  type="password"
                  autoComplete="off"
                  placeholder="eyJhIjoi…"
                  value={token}
                  onChange={(e) => setToken(e.target.value)}
                />
              </Field>
              <div className="flex gap-2">
                <Button size="sm" type="submit" disabled={save.isPending || !hostname.trim() || (!tunnel.namedHostname && !token.trim())}>
                  {t('defaults.phone.useTunnel')}
                </Button>
                <Button size="sm" variant="ghost" type="button" onClick={() => setEditing(false)}>
                  {t('common.cancel')}
                </Button>
              </div>
            </form>
          ) : (
            tunnel.named && (
              <div className="flex gap-2">
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => {
                    setHostname(tunnel.hostname ?? '');
                    setEditing(true);
                  }}
                >
                  {t('defaults.phone.changeTunnel')}
                </Button>
              </div>
            )
          )}
        </div>
      )}
    </SettingRow>
  );
}

function PairingCode({ pairing, onAgain, onDone }: { pairing: T.LANPairing; onAgain: () => void; onDone: () => void }) {
  const t = useT();
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  const left = Math.max(0, new Date(pairing.expires).getTime() - now);
  const expired = left === 0;
  return (
    <div className="flex flex-col items-center gap-4 rounded-2xl border border-line bg-surface-faint p-5 sm:flex-row sm:items-start" data-phone-qr>
      <div className={expired ? 'opacity-20' : ''}>
        <QRCode rows={pairing.qr} size={184} />
      </div>
      <div className="grid min-w-0 flex-1 gap-2 text-[13px] leading-relaxed text-muted">
        <p className="font-medium text-primary">{t('defaults.phone.scan')}</p>
        <p>{expired ? t('defaults.phone.codeExpired') : t('defaults.phone.codeValid', { minutes: Math.ceil(left / 60_000) })}</p>
        <p className="break-all font-mono text-[11px] text-subtle" data-phone-url>{pairing.urls[0]}</p>
        <div className="flex flex-wrap gap-2 pt-1">
          <Button
            size="sm"
            variant="secondary"
            onClick={() => {
              window.agentbox.copyText(pairing.urls[0]);
              toast(t('common.copied'), { description: pairing.urls[0] });
            }}
            data-phone-copy
          >
            {t('defaults.phone.copyAddress')}
          </Button>
          <Button size="sm" variant="secondary" onClick={onAgain}>
            {t('defaults.phone.newCode')}
          </Button>
          <Button size="sm" variant="ghost" onClick={onDone}>
            {t('common.done')}
          </Button>
        </div>
      </div>
    </div>
  );
}

// QRCode draws a QR code from the daemon's rows of '1' and '0', dark on white
// with the quiet zone around it that scanners need, whatever the theme.
export function QRCode({ rows, size }: { rows: string[]; size: number }) {
  const t = useT();
  const quiet = 3;
  const n = rows.length + 2 * quiet;
  let path = '';
  rows.forEach((row, y) => {
    for (let x = 0; x < row.length; x++) if (row[x] === '1') path += `M${x + quiet} ${y + quiet}h1v1h-1z`;
  });
  return (
    <svg width={size} height={size} viewBox={`0 0 ${n} ${n}`} shapeRendering="crispEdges" role="img" aria-label={t('defaults.phone.qrLabel')} className="rounded-lg">
      <rect width={n} height={n} fill="#fff" />
      <path d={path} fill="#000" />
    </svg>
  );
}

function PairedPhones() {
  const t = useT();
  const lan = useLAN();
  const phones = lan.data?.phones ?? [];
  return (
    <SettingRow label={t('defaults.phone.pairedLabel')} description={t('defaults.phone.pairedDescription')}>
      {phones.length === 0 ? (
        <SettingNote>{t('defaults.phone.noneYet')}</SettingNote>
      ) : (
        <ul className="grid gap-2" data-phones>
          {phones.map((p) => (
            <PhoneRow key={p.id} phone={p} />
          ))}
        </ul>
      )}
    </SettingRow>
  );
}

// The scopes a phone's row toggles directly; merge has a switch of its own,
// turned on only after a confirmation.
const plainScopes = ['read', 'chat', 'answer'] as const;
const scopeLabels: Record<(typeof plainScopes)[number], MessageKey> = {
  read: 'defaults.phone.scope.read',
  chat: 'defaults.phone.scope.chat',
  answer: 'defaults.phone.scope.answer',
};

function PhoneRow({ phone: p }: { phone: T.LANPhone }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [confirmMerge, setConfirmMerge] = useState(false);
  const [showAudit, setShowAudit] = useState(false);
  const revoke = useMutation({
    mutationFn: () => api.removeLANPhone(p.id),
    onSuccess: () => {
      toast(t('defaults.phone.unpairedToast', { name: p.name }));
      void queryClient.invalidateQueries({ queryKey: ['lan'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const scopes = useMutation({
    mutationFn: (next: string[]) => api.updateLANPhone(p.id, next),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['lan'] }),
    onError: (err) => toast.error(errorMessage(err)),
  });
  // A daemon from before scopes sends none.
  const current = p.scopes ?? [];
  const has = (scope: string) => current.includes(scope);
  const toggle = (scope: string, on: boolean) => scopes.mutateAsync(on ? [...current, scope] : current.filter((s) => s !== scope));
  return (
    <li className="grid gap-3 rounded-xl border border-line px-3 py-2.5" data-phone={p.id}>
      <div className="flex items-center gap-3">
        <Smartphone className="size-4 shrink-0 text-subtle" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-[13px] font-medium text-primary">{p.name}</p>
          <p className="truncate text-[11px] text-subtle">
            {t('defaults.phone.pairedOn', { date: formatDate(new Date(p.paired)) })}
            {p.lastSeen && ` · ${t('defaults.phone.lastSeen', { when: formatDateTime(new Date(p.lastSeen)) })}`}
            {p.lastAddr && ` · ${t('defaults.phone.fromAddr', { addr: p.lastAddr })}`}
          </p>
        </div>
        <Button size="sm" variant="ghost" disabled={revoke.isPending} onClick={() => revoke.mutate()}>
          {t('defaults.phone.unpair')}
        </Button>
      </div>
      <div className="flex min-w-0 flex-wrap items-center gap-1.5 pl-7" data-phone-scopes>
        <span className="mr-1 text-[11px] text-subtle">{t('defaults.phone.mayLabel')}</span>
        {plainScopes.map((scope) => (
          <button
            key={scope}
            type="button"
            aria-pressed={has(scope)}
            data-scope={scope}
            disabled={scopes.isPending}
            onClick={() => void toggle(scope, !has(scope))}
            className={cn(
              'rounded-full border px-2.5 py-0.5 text-[11px] transition-colors disabled:opacity-60',
              has(scope) ? 'border-brand-400/50 bg-brand-500/15 text-primary' : 'border-line text-subtle line-through decoration-faint hover:text-secondary',
            )}
          >
            {t(scopeLabels[scope])}
          </button>
        ))}
      </div>
      <div className="flex min-w-0 items-start gap-3 pl-7">
        <div className="min-w-0 flex-1">
          <p className="text-[12px] text-primary">{t('defaults.phone.mergeLabel')}</p>
          <p className="text-[11px] leading-relaxed text-subtle">{t('defaults.phone.mergeDescription')}</p>
        </div>
        <Switch
          data-phone-merge
          aria-label={t('defaults.phone.mergeLabel')}
          disabled={scopes.isPending}
          checked={has('merge')}
          onCheckedChange={(on) => (on ? setConfirmMerge(true) : void toggle('merge', false))}
        />
      </div>
      <div className="pl-7">
        <button type="button" data-phone-audit-toggle className="text-[11px] text-secondary underline-offset-2 hover:underline" onClick={() => setShowAudit((v) => !v)}>
          {showAudit ? t('defaults.phone.hideAudit') : t('defaults.phone.showAudit')}
        </button>
        {showAudit && <PhoneAudit phone={p} />}
      </div>
      <ConfirmDialog
        open={confirmMerge}
        onOpenChange={setConfirmMerge}
        title={t('defaults.phone.mergeConfirmTitle', { name: p.name })}
        description={t('defaults.phone.mergeConfirmDescription')}
        confirmLabel={t('defaults.phone.mergeConfirm')}
        destructive
        onConfirm={() => toggle('merge', true)}
      />
    </li>
  );
}

// PhoneAudit is every write a phone made or tried, newest first: what the
// daemon keeps in phone_audit, which nothing edits or deletes.
function PhoneAudit({ phone }: { phone: T.LANPhone }) {
  const t = useT();
  const audit = useInfiniteQuery({
    queryKey: ['lan', 'audit', phone.id],
    queryFn: ({ pageParam }) => api.lanPhoneAudit(phone.id, pageParam),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.more ? last.entries[last.entries.length - 1]?.id : undefined),
  });
  const entries = audit.data?.pages.flatMap((page) => page.entries) ?? [];
  if (audit.isPending) return <SettingNote className="pt-2">{t('common.loading')}</SettingNote>;
  if (audit.error) return <SettingNote tone="error" className="pt-2">{errorMessage(audit.error)}</SettingNote>;
  if (entries.length === 0) return <SettingNote className="pt-2">{t('defaults.phone.auditEmpty')}</SettingNote>;
  return (
    <div className="grid gap-1 pt-2" data-phone-audit>
      <ul className="grid max-h-64 gap-0.5 overflow-y-auto rounded-lg border border-line bg-surface-faint p-1.5">
        {entries.map((e) => (
          <li key={e.id} className="flex min-w-0 items-baseline gap-2 px-1 py-0.5 text-[11px]" data-audit-result={e.result}>
            <span className={cn('w-14 shrink-0 font-medium', { ok: 'text-emerald-300', refused: 'text-rose-300', failed: 'text-amber-300' }[e.result] ?? 'text-subtle')}>
              {e.result === 'ok' ? t('defaults.phone.auditOk') : e.result === 'refused' ? t('defaults.phone.auditRefused') : t('defaults.phone.auditFailed')}
            </span>
            <span className="min-w-0 flex-1 truncate font-mono text-secondary" title={e.route}>
              {e.route}
            </span>
            <span className="shrink-0 text-faint">{e.scope || t('defaults.phone.auditNever')}</span>
            <span className="shrink-0 text-faint" title={e.addr}>
              {formatDateTime(new Date(e.at))}
            </span>
          </li>
        ))}
      </ul>
      {audit.hasNextPage && (
        <div>
          <Button size="sm" variant="ghost" disabled={audit.isFetchingNextPage} onClick={() => void audit.fetchNextPage()}>
            {t('defaults.phone.auditMore')}
          </Button>
        </div>
      )}
    </div>
  );
}
