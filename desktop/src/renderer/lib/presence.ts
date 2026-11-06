// Presence: this window's lease on what it shows (api.PresenceRequest), so the
// daemon spends its background work where it is seen. It samples an agent's
// live resources only while a view asks for them (the panel's Resources), and
// backs its housekeeping off for projects no window shows, or for everything
// while the computer is on battery or hot (main/hostpower.ts).
//
// Components say what they show with usePresence; startPresence, once per
// window, renews the lease every third of its TTL and at once when what is
// shown changes, sending nothing while the window is hidden. A window that
// closes gives its lease up; one that crashes has it lapse on its own.
import { useEffect } from 'react';
import type * as T from '../../shared/api';
import { api } from './api.ts';

const shown = new Map<number, T.PresenceView>();
let nextKey = 1;
let changed: (() => void) | undefined;

// mergeViews is the lease's views: one per project and agent, asking for
// resources if any of its views does, none while the window is hidden.
export function mergeViews(views: Iterable<T.PresenceView>, hidden: boolean): T.PresenceView[] {
  if (hidden) return [];
  const out = new Map<string, T.PresenceView>();
  for (const v of views) {
    if (!v.project) continue;
    const key = `${v.project}\n${v.agent ?? ''}`;
    const had = out.get(key);
    out.set(key, { project: v.project, ...(v.agent ? { agent: v.agent } : {}), ...(v.resources || had?.resources ? { resources: true } : {}) });
  }
  return [...out.values()].sort((a, b) => `${a.project}\n${a.agent ?? ''}`.localeCompare(`${b.project}\n${b.agent ?? ''}`));
}

// usePresence puts view in the lease while the component shows it.
export function usePresence(view: T.PresenceView | undefined): void {
  const project = view?.project;
  const agent = view?.agent;
  const resources = view?.resources === true;
  useEffect(() => {
    if (!project) return;
    const key = nextKey++;
    shown.set(key, { project, ...(agent ? { agent } : {}), ...(resources ? { resources } : {}) });
    changed?.();
    return () => {
      shown.delete(key);
      changed?.();
    };
  }, [project, agent, resources]);
}

// The default renewal, until the daemon says its TTL: a third of 45 seconds.
const defaultRenewMs = 15_000;

// startPresence keeps this window's lease; the function it returns stops it.
export function startPresence(): () => void {
  const bridge = window.agentbox;
  const web = 'web' in bridge;
  const client = `${web ? 'web' : 'desktop'}-${crypto.randomUUID()}`;
  let power: T.HostPower | undefined;
  let last = '';
  let timer: ReturnType<typeof setTimeout> | undefined;
  let every = defaultRenewMs;
  let lastAt = 0;
  let stopped = false;

  const renew = async (force: boolean) => {
    if (stopped) return;
    const req: T.PresenceRequest = { client, kind: web ? 'web' : 'desktop', visible: !document.hidden, views: mergeViews(shown.values(), document.hidden), ...(power ? { power } : {}) };
    const body = JSON.stringify(req);
    if (!force && body === last && Date.now() - lastAt < every) return;
    last = body;
    lastAt = Date.now();
    try {
      const res = await api.renewPresence(req);
      if (res.ttlSeconds > 0) every = (res.ttlSeconds * 1000) / 3;
    } catch {
      // A daemon from before presence, or one not answering: the next
      // renewal tries again.
      last = '';
    }
  };
  // Several changes in a row (switching pages) make one renewal.
  const soon = () => {
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => void renew(false), 150);
  };
  changed = soon;
  const tick = setInterval(() => void renew(true), defaultRenewMs);
  const offPower = web
    ? () => {}
    : bridge.onHostPower((next) => {
        power = next;
        soon();
      });
  if (!web) {
    void bridge
      .hostPower()
      .then((p) => {
        power = p;
        soon();
      })
      .catch(() => {});
  }
  const onVisibility = () => soon();
  const onUnload = () => void api.endPresence(client).catch(() => {});
  document.addEventListener('visibilitychange', onVisibility);
  window.addEventListener('beforeunload', onUnload);
  soon();
  return () => {
    stopped = true;
    changed = undefined;
    if (timer) clearTimeout(timer);
    clearInterval(tick);
    offPower();
    document.removeEventListener('visibilitychange', onVisibility);
    window.removeEventListener('beforeunload', onUnload);
    onUnload();
  };
}
