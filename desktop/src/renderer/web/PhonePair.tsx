// Before a phone can see anything, it pairs (internal/daemon/lan.go): the QR
// code AgentBox shows on the computer opens this page with a one-time secret
// in its fragment, which is posted back to the daemon for a cookie of the
// phone's own. A phone that comes without one, or whose pairing was revoked,
// is told how to pair.
import { Smartphone, TriangleAlert } from 'lucide-react';
import type * as T from '../../shared/api';
import { phoneName } from './phoneName.ts';

// pairFromURL pairs with the secret in the page's address, if there's one, and
// takes it out of the address either way: it works once.
export async function pairFromURL(): Promise<{ session?: T.LANSession; error?: string } | null> {
  const secret = /[#&]pair=([^&]+)/.exec(location.hash)?.[1];
  if (!secret) return null;
  history.replaceState(null, '', location.pathname + location.search);
  try {
    const res = await fetch('/lan/pair', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ secret, name: phoneName() } satisfies T.LANPairRequest),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) return { error: (body as T.Error).error ?? `HTTP ${res.status}` };
    return { session: body as T.LANSession };
  } catch (err) {
    return { error: err instanceof Error ? err.message : String(err) };
  }
}

export async function currentSession(): Promise<T.LANSession | null> {
  const res = await fetch('/lan/session', { credentials: 'same-origin' });
  if (res.status === 401) return null;
  if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
  return (await res.json()) as T.LANSession;
}

export function PlainHTTPNote({ className = '' }: { className?: string }) {
  return (
    <p className={`text-[12px] leading-relaxed text-subtle ${className}`}>
      This connection is plain HTTP on your local network, not encrypted: use it on a network you trust.
    </p>
  );
}

export function NotPaired({ error }: { error?: string }) {
  return (
    <div className="flex min-h-dvh items-center justify-center px-6 py-10">
      <div className="grid w-full max-w-sm gap-5" data-phone-unpaired>
        <div className="flex size-12 items-center justify-center rounded-2xl bg-surface-raised text-brand-300">
          <Smartphone className="size-6" />
        </div>
        <div className="grid gap-2">
          <h1 className="text-xl font-semibold tracking-tight text-title">Pair this phone with AgentBox</h1>
          <p className="text-[14px] leading-relaxed text-muted">
            On your computer, open AgentBox's <span className="text-primary">Settings</span>, then <span className="text-primary">Phone</span>, turn on{' '}
            <span className="text-primary">Chat from your phone</span>, and scan the QR code with this phone's camera.
          </p>
          <p className="text-[14px] leading-relaxed text-muted">
            Or, in a terminal there: <span className="font-mono text-[13px] text-primary">agentbox phone pair</span>
          </p>
        </div>
        {error && (
          <div className="flex gap-2 rounded-xl border border-rose-500/30 bg-rose-500/10 px-3 py-2.5 text-[13px] text-rose-200">
            <TriangleAlert className="mt-0.5 size-4 shrink-0" />
            <span>Couldn't pair: {error}</span>
          </div>
        )}
        <PlainHTTPNote />
      </div>
    </div>
  );
}
