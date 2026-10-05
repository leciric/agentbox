import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Globe, Smartphone } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatDate, formatDateTime, t, useT } from '../lib/i18n';
import type { SettingGroup } from '../lib/settingsSearch';
import { errorMessage } from '../lib/utils';
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
        <Switch
          data-phone-chat
          aria-label={t('defaults.phone.chatLabel')}
          disabled={save.isPending || !st}
          checked={st?.enabled ?? false}
          onCheckedChange={(next) => {
            wantQR.current = next;
            save.mutate(next);
          }}
        />
      }
    >
      {st?.enabled && (
        <div className="grid gap-3">
          <SettingNote tone="warning">{t('defaults.phone.plainHttp')}</SettingNote>
          {st.tunnel.url && !st.listening ? null : st.listening && st.urls.length > 0 ? (
            <SettingNote>
              {st.urls.length > 1
                ? t.rich('defaults.phone.phonesOpenMore', { url: (c) => <span className="font-mono text-primary">{c}</span>, address: st.urls[0], others: st.urls.slice(1).join(', ') })
                : t.rich('defaults.phone.phonesOpen', { url: (c) => <span className="font-mono text-primary">{c}</span>, address: st.urls[0] })}
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
          onCheckedChange={(next) => save.mutate(next ? { enabled: true, tunnel: true } : { tunnel: false })}
        />
      }
    >
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
                {t.rich(tunnel.named ? 'defaults.phone.tunnelOpenNamed' : 'defaults.phone.tunnelOpenQuick', {
                  url: (c) => <span className="font-mono text-primary">{c}</span>,
                  address: tunnel.url,
                })}
              </SettingNote>
            ) : (
              <SettingNote tone={tunnel.state === 'failed' ? 'error' : 'muted'}>{tunnel.error || t('defaults.phone.startingTunnel')}</SettingNote>
            ))}
          {tunnel.named && !editing && (
            <SettingNote>
              {t.rich('defaults.phone.namedTunnel', {
                mono: (c) => <span className="font-mono text-primary">{c}</span>,
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
                save.mutate({ tunnelHostname: hostname, ...(token || !tunnel.named ? { tunnelToken: token } : {}) });
              }}
            >
              <SettingNote>
                {t.rich('defaults.phone.makeTunnel', { mono: (c) => <span className="font-mono text-primary">{c}</span>, origin: tunnel.origin })}
              </SettingNote>
              <Field label={t('defaults.phone.publicHostname')} htmlFor="tunnel-hostname">
                <Input id="tunnel-hostname" placeholder="chat.example.com" value={hostname} onChange={(e) => setHostname(e.target.value)} />
              </Field>
              <Field label={t('defaults.phone.token')} htmlFor="tunnel-token" hint={tunnel.named ? t('defaults.phone.tokenKeep') : t('defaults.phone.tokenNew')}>
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
                <Button size="sm" type="submit" disabled={save.isPending || !hostname.trim() || (!tunnel.named && !token.trim())}>
                  {t('defaults.phone.useTunnel')}
                </Button>
                <Button size="sm" variant="ghost" type="button" onClick={() => setEditing(false)}>
                  {t('common.cancel')}
                </Button>
              </div>
            </form>
          ) : (
            <div className="flex gap-2">
              <Button
                size="sm"
                variant="secondary"
                onClick={() => {
                  setHostname(tunnel.hostname ?? '');
                  setEditing(true);
                }}
              >
                {tunnel.named ? t('defaults.phone.changeTunnel') : t('defaults.phone.ownTunnel')}
              </Button>
              {tunnel.named && (
                <Button size="sm" variant="ghost" disabled={save.isPending} onClick={() => save.mutate({ tunnelToken: '' })}>
                  {t('defaults.phone.quickInstead')}
                </Button>
              )}
            </div>
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
        <p className="break-all font-mono text-[11px] text-subtle">{pairing.urls[0]}</p>
        <div className="flex gap-2 pt-1">
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
  const queryClient = useQueryClient();
  const revoke = useMutation({
    mutationFn: (phone: T.LANPhone) => api.removeLANPhone(phone.id),
    onSuccess: (_, phone) => {
      toast(t('defaults.phone.unpairedToast', { name: phone.name }));
      void queryClient.invalidateQueries({ queryKey: ['lan'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const phones = lan.data?.phones ?? [];
  return (
    <SettingRow label={t('defaults.phone.pairedLabel')} description={t('defaults.phone.pairedDescription')}>
      {phones.length === 0 ? (
        <SettingNote>{t('defaults.phone.noneYet')}</SettingNote>
      ) : (
        <ul className="grid gap-2" data-phones>
          {phones.map((p) => (
            <li key={p.id} className="flex items-center gap-3 rounded-xl border border-line px-3 py-2.5">
              <Smartphone className="size-4 shrink-0 text-subtle" />
              <div className="min-w-0 flex-1">
                <p className="truncate text-[13px] font-medium text-primary">{p.name}</p>
                <p className="truncate text-[11px] text-subtle">
                  {t('defaults.phone.pairedOn', { date: formatDate(new Date(p.paired)) })}
                  {p.lastSeen && ` · ${t('defaults.phone.lastSeen', { when: formatDateTime(new Date(p.lastSeen)) })}`}
                  {p.lastAddr && ` · ${t('defaults.phone.fromAddr', { addr: p.lastAddr })}`}
                </p>
              </div>
              <Button size="sm" variant="ghost" disabled={revoke.isPending} onClick={() => revoke.mutate(p)}>
                {t('defaults.phone.unpair')}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </SettingRow>
  );
}
