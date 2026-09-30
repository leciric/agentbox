import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Globe, Smartphone } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
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
      title: 'Your phone',
      entries: [
        {
          id: 'phone-chat',
          label: 'Chat from your phone',
          keywords: 'phone mobile lan network wifi qr code pair browser remote',
          render: () => <PhoneChat />,
        },
        {
          id: 'phone-tunnel',
          label: 'Reach from anywhere',
          keywords: 'phone mobile tunnel cloudflare cloudflared internet anywhere remote https trycloudflare',
          render: () => <PhoneTunnel />,
        },
        {
          id: 'phones',
          label: 'Paired phones',
          keywords: 'phone mobile revoke unpair devices',
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
  const lan = useLAN();
  const queryClient = useQueryClient();
  const [pairing, setPairing] = useState<T.LANPairing | null>(null);
  const pair = useMutation({
    mutationFn: api.pairLAN,
    onSuccess: setPairing,
    onError: (err) => toast.error("Couldn't make a QR code", { description: errorMessage(err) }),
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
      toast.success(`Paired ${st?.phones[0]?.name ?? 'your phone'}`);
    }
    before.current = phones;
  }, [phones, pairing, st]);

  return (
    <SettingRow
      label="Chat from your phone"
      description="Read and send chat messages from your phone's browser, on the same network as this computer."
      details={
        <>
          Turned on, AgentBox opens port {st?.port ?? 7780} on this computer's network, and shows a QR code: scanning it with the phone's
          camera pairs that phone, and nothing works from one that isn't paired. A paired phone can read and answer your chats, the
          project's and each agent's, and start a stopped agent to chat with it; nothing else of AgentBox. It's plain HTTP, without
          encryption, so someone else on the same network could read the chats as they go by: use it on a network you trust.
        </>
      }
      control={
        <Switch
          data-phone-chat
          aria-label="Chat from your phone"
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
          <SettingNote tone="warning">Plain HTTP on your local network, not encrypted: use it on a network you trust.</SettingNote>
          {st.tunnel.url && !st.listening ? null : st.listening && st.urls.length > 0 ? (
            <SettingNote>
              Phones open <span className="font-mono text-primary">{st.urls[0]}</span>
              {st.urls.length > 1 && <> (or {st.urls.slice(1).join(', ')})</>}.
            </SettingNote>
          ) : (
            <SettingNote tone={st.error?.startsWith('waiting') ? 'muted' : 'error'}>{st.error || 'Opening the port…'}</SettingNote>
          )}
          {!st.webVersion && <SettingNote>Getting the page phones are shown ready…</SettingNote>}
          {pairing ? (
            <PairingCode pairing={pairing} onAgain={() => pair.mutate()} onDone={() => setPairing(null)} />
          ) : (
            (st.listening || st.tunnel.url) &&
            st.urls.length > 0 && (
              <div>
                <Button size="sm" variant="secondary" data-phone-pair disabled={pair.isPending} onClick={() => pair.mutate()}>
                  <Smartphone className="size-3.5" /> Pair a phone
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
      label="Reach from anywhere"
      description="Chat from your phone away from this network too, through a Cloudflare Tunnel."
      details={
        <>
          Turned on, AgentBox runs Cloudflare's cloudflared, downloaded the first time, and puts the phone page on an https address on the
          internet, with no port opened on this computer. By default it's a quick tunnel: no Cloudflare account, and an address that
          changes each time AgentBox starts, so phones pair again then. Your own named tunnel keeps its address. Anyone who has the address
          can open the pairing page; only a phone you pair with a QR code gets further, and even then it reaches only your chats.
        </>
      }
      control={
        <Switch
          data-phone-tunnel
          aria-label="Reach from anywhere"
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
              This puts the pairing page on the internet. Pairing is still needed, and wrong codes are held back, but only turn it on while you
              want it.
            </SettingNote>
          )}
          {on &&
            (tunnel.state === 'running' && tunnel.url ? (
              <SettingNote>
                <Globe className="mr-1 inline size-3.5 align-[-2px]" />
                Phones open <span className="font-mono text-primary">{tunnel.url}</span> from anywhere
                {tunnel.named ? '.' : ' — a quick tunnel, whose address changes when AgentBox starts again.'}
              </SettingNote>
            ) : (
              <SettingNote tone={tunnel.state === 'failed' ? 'error' : 'muted'}>{tunnel.error || 'Starting the tunnel…'}</SettingNote>
            ))}
          {tunnel.named && !editing && (
            <SettingNote>
              Your tunnel for <span className="font-mono text-primary">{tunnel.hostname}</span>: in Cloudflare's dashboard, its public hostname
              points at <span className="font-mono text-primary">{tunnel.origin}</span>.
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
                Make a tunnel in Cloudflare's dashboard (Networks → Tunnels), give it a public hostname whose service is{' '}
                <span className="font-mono text-primary">{tunnel.origin}</span>, and paste its token here.
              </SettingNote>
              <Field label="Public hostname" htmlFor="tunnel-hostname">
                <Input id="tunnel-hostname" placeholder="chat.example.com" value={hostname} onChange={(e) => setHostname(e.target.value)} />
              </Field>
              <Field label="Token" htmlFor="tunnel-token" hint={tunnel.named ? 'Leave it empty to keep the one saved.' : 'Kept encrypted, and never shown again.'}>
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
                  Use this tunnel
                </Button>
                <Button size="sm" variant="ghost" type="button" onClick={() => setEditing(false)}>
                  Cancel
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
                {tunnel.named ? 'Change your tunnel' : 'Use your own Cloudflare tunnel'}
              </Button>
              {tunnel.named && (
                <Button size="sm" variant="ghost" disabled={save.isPending} onClick={() => save.mutate({ tunnelToken: '' })}>
                  Use a quick tunnel instead
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
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  const left = Math.max(0, new Date(pairing.expires).getTime() - now);
  const expired = left === 0;
  return (
    <div className="flex flex-col items-center gap-4 rounded-2xl border border-line bg-surface-faint p-5 sm:flex-row sm:items-start" data-phone-qr>
      <div className={expired ? 'opacity-20' : ''}>
        <QRCode rows={pairing.qr} size={184} />
      </div>
      <div className="grid min-w-0 flex-1 gap-2 text-[13px] leading-relaxed text-muted">
        <p className="font-medium text-primary">Scan this with your phone's camera</p>
        <p>
          It pairs one phone, {expired ? 'and it has expired' : `for the next ${Math.ceil(left / 60_000)} minute${left > 60_000 ? 's' : ''}`}. Or open
          this on the phone:
        </p>
        <p className="break-all font-mono text-[11px] text-subtle">{pairing.urls[0]}</p>
        <div className="flex gap-2 pt-1">
          <Button size="sm" variant="secondary" onClick={onAgain}>
            New code
          </Button>
          <Button size="sm" variant="ghost" onClick={onDone}>
            Done
          </Button>
        </div>
      </div>
    </div>
  );
}

// QRCode draws a QR code from the daemon's rows of '1' and '0', dark on white
// with the quiet zone around it that scanners need, whatever the theme.
export function QRCode({ rows, size }: { rows: string[]; size: number }) {
  const quiet = 3;
  const n = rows.length + 2 * quiet;
  let path = '';
  rows.forEach((row, y) => {
    for (let x = 0; x < row.length; x++) if (row[x] === '1') path += `M${x + quiet} ${y + quiet}h1v1h-1z`;
  });
  return (
    <svg width={size} height={size} viewBox={`0 0 ${n} ${n}`} shapeRendering="crispEdges" role="img" aria-label="QR code to pair a phone" className="rounded-lg">
      <rect width={n} height={n} fill="#fff" />
      <path d={path} fill="#000" />
    </svg>
  );
}

function PairedPhones() {
  const lan = useLAN();
  const queryClient = useQueryClient();
  const revoke = useMutation({
    mutationFn: (phone: T.LANPhone) => api.removeLANPhone(phone.id),
    onSuccess: (_, phone) => {
      toast(`Unpaired ${phone.name}`);
      void queryClient.invalidateQueries({ queryKey: ['lan'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const phones = lan.data?.phones ?? [];
  return (
    <SettingRow label="Paired phones" description="Unpairing a phone cuts it off at once; it needs a new QR code to come back.">
      {phones.length === 0 ? (
        <SettingNote>No phone is paired.</SettingNote>
      ) : (
        <ul className="grid gap-2" data-phones>
          {phones.map((p) => (
            <li key={p.id} className="flex items-center gap-3 rounded-xl border border-line px-3 py-2.5">
              <Smartphone className="size-4 shrink-0 text-subtle" />
              <div className="min-w-0 flex-1">
                <p className="truncate text-[13px] font-medium text-primary">{p.name}</p>
                <p className="truncate text-[11px] text-subtle">
                  Paired {new Date(p.paired).toLocaleDateString()}
                  {p.lastSeen && ` · last seen ${new Date(p.lastSeen).toLocaleString()}`}
                  {p.lastAddr && ` from ${p.lastAddr}`}
                </p>
              </div>
              <Button size="sm" variant="ghost" disabled={revoke.isPending} onClick={() => revoke.mutate(p)}>
                Unpair
              </Button>
            </li>
          ))}
        </ul>
      )}
    </SettingRow>
  );
}
