import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Copy, Link2, Monitor, Share2 } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatDate, formatDateTime, t, useT } from '../lib/i18n';
import type { SettingGroup } from '../lib/settingsSearch';
import { cn, errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Field, Input } from './ui/input';
import { Select, SelectOption } from './ui/select';
import { SettingNote, SettingRow } from './ui/settings';

// Sharing projects with another AgentBox (internal/daemon/sharepeers.go and
// shareremote.go): this one pairs another with a one-time link, through the
// same page phones use, and chooses which projects it reaches and how many
// agents it may run here; or it pastes another's link, and shows the projects
// that one shares in its sidebar, under Remote.

export function shareGroups(): SettingGroup[] {
  return [
    {
      id: 'share',
      title: t('peers.title'),
      entries: [
        {
          id: 'share-peers',
          description: t('peers.peersDescription'),
          label: t('peers.peersLabel'),
          keywords: t('peers.peersKeywords'),
          render: () => <SharePeers />,
        },
        {
          id: 'share-join',
          description: t('peers.joinDescription'),
          label: t('peers.joinLabel'),
          keywords: t('peers.joinKeywords'),
          render: () => <ShareJoin />,
        },
      ],
    },
  ];
}

function useShare() {
  return useQuery({ queryKey: ['share'], queryFn: api.share });
}

function SharePeers() {
  const t = useT();
  const share = useShare();
  const queryClient = useQueryClient();
  const st = share.data;
  const [pairing, setPairing] = useState<T.SharePairing | null>(null);
  const [name, setName] = useState<string | null>(null);
  const pair = useMutation({
    mutationFn: api.pairShare,
    onSuccess: setPairing,
    onError: (err) => toast.error(t('peers.pairFailed'), { description: errorMessage(err) }),
  });
  const save = useMutation({
    mutationFn: (req: T.UpdateShareRequest) => api.updateShare(req),
    onSuccess: (next) => queryClient.setQueryData(['share'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });
  // The link goes once another AgentBox pairs with it.
  const peers = st?.peers.length ?? 0;
  const before = useRef(peers);
  useEffect(() => {
    if (peers > before.current && pairing) {
      setPairing(null);
      if (st?.peers[0]) toast.success(t('peers.pairedToast', { name: st.peers[0].name }));
    }
    before.current = peers;
  }, [peers, pairing, st, t]);
  if (!st) return null;
  return (
    <SettingRow label={t('peers.peersLabel')} description={t('peers.peersDescription')}>
      <div className="grid gap-3" data-share-peers>
        <SettingNote tone="warning">{t('peers.accountsWarning')}</SettingNote>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t('peers.nameLabel')} htmlFor="share-name">
            <Input
              id="share-name"
              data-share-name
              placeholder={t('peers.namePlaceholder')}
              value={name ?? st.name}
              onChange={(e) => setName(e.target.value)}
              onBlur={() => {
                if (name !== null && name !== st.name) save.mutate({ name });
                setName(null);
              }}
            />
          </Field>
          <Field label={t('peers.slotsLabel')} htmlFor="share-slots" hint={t('peers.slotsDetails')}>
            <Input
              id="share-slots"
              data-share-slots
              type="number"
              min={0}
              max={64}
              value={st.slots}
              onChange={(e) => {
                const n = Number(e.target.value);
                if (Number.isInteger(n) && n >= 0 && n <= 64) save.mutate({ slots: n });
              }}
            />
          </Field>
        </div>
        {!st.ready && st.error && <SettingNote tone="muted">{st.error}</SettingNote>}
        {pairing ? (
          <PairingLink pairing={pairing} onDone={() => setPairing(null)} />
        ) : (
          <div>
            <Button size="sm" variant="secondary" data-share-pair disabled={!st.ready || pair.isPending} onClick={() => pair.mutate()}>
              <Share2 className="size-3.5" /> {t('peers.pair')}
            </Button>
          </div>
        )}
        {st.peers.length === 0 ? (
          <SettingNote>{t('peers.noPeers')}</SettingNote>
        ) : (
          <ul className="grid gap-2" data-share-peer-list>
            {st.peers.map((p) => (
              <PeerRow key={p.id} peer={p} slots={st.slots} />
            ))}
          </ul>
        )}
      </div>
    </SettingRow>
  );
}

function PairingLink({ pairing, onDone }: { pairing: T.SharePairing; onDone: () => void }) {
  const t = useT();
  const copy = () =>
    void navigator.clipboard.writeText(pairing.link).then(
      () => toast.success(t('peers.copied')),
      (err: unknown) => toast.error(errorMessage(err)),
    );
  return (
    <div className="grid min-w-0 gap-2 rounded-xl border border-line px-3 py-2.5" data-share-link>
      <div className="flex min-w-0 items-center gap-2">
        <Link2 className="size-4 shrink-0 text-subtle" />
        <code className="min-w-0 flex-1 break-all font-mono text-caption text-primary [overflow-wrap:anywhere]" data-share-link-text>
          {pairing.link}
        </code>
        <Button size="sm" variant="secondary" className="shrink-0" onClick={copy}>
          <Copy className="size-3.5" /> {t('peers.copy')}
        </Button>
      </div>
      <p className="text-caption text-subtle">{t('peers.linkNote', { time: formatDateTime(new Date(pairing.expires)) })}</p>
      <div>
        <Button size="sm" variant="ghost" onClick={onDone}>
          {t('common.done')}
        </Button>
      </div>
    </div>
  );
}

function PeerRow({ peer: p, slots }: { peer: T.SharePeer; slots: number }) {
  const t = useT();
  const queryClient = useQueryClient();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const local = (projects.data ?? []).filter((x) => !x.remote);
  const update = useMutation({
    mutationFn: (req: T.UpdateSharePeerRequest) => api.updateSharePeer(p.id, req),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['share'] }),
    onError: (err) => toast.error(errorMessage(err)),
  });
  const revoke = useMutation({
    mutationFn: () => api.removeSharePeer(p.id),
    onSuccess: () => {
      toast(t('peers.unpairedToast', { name: p.name }));
      void queryClient.invalidateQueries({ queryKey: ['share'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const shared = p.projects ?? [];
  const toggle = (name: string) => update.mutate({ projects: shared.includes(name) ? shared.filter((x) => x !== name) : [...shared, name] });
  const limit = p.slots < 0 ? slots : p.slots;
  const chip = (on: boolean) =>
    cn(
      'rounded-full border px-2.5 py-0.5 text-caption transition-colors disabled:opacity-60',
      on ? 'border-brand-400/50 bg-brand-500/15 text-primary' : 'border-line bg-surface-raised text-subtle hover:text-secondary',
    );
  return (
    <li className="grid min-w-0 gap-3 rounded-xl border border-line px-3 py-2.5" data-share-peer={p.id}>
      <div className="flex min-w-0 items-center gap-3">
        <Monitor className="size-4 shrink-0 text-subtle" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-ui font-medium text-primary">{p.name}</p>
          <p className="break-words text-caption text-subtle [overflow-wrap:anywhere]">
            {t('defaults.phone.pairedOn', { date: formatDate(new Date(p.paired)) })}
            {p.lastSeen && ` · ${t('defaults.phone.lastSeen', { when: formatDateTime(new Date(p.lastSeen)) })}`}
            {p.lastAddr && ` · ${t('defaults.phone.fromAddr', { addr: p.lastAddr })}`}
            {` · ${t('peers.running', { running: p.running, slots: limit })}`}
          </p>
        </div>
        <Button size="sm" variant="ghost" className="shrink-0" disabled={revoke.isPending} onClick={() => revoke.mutate()}>
          {t('defaults.phone.unpair')}
        </Button>
      </div>
      <div className="flex min-w-0 flex-wrap items-center gap-1.5 pl-7" data-share-projects>
        <span className="mr-1 text-caption text-subtle">{t('peers.reaches')}</span>
        <button type="button" aria-pressed={p.allProjects} data-share-all disabled={update.isPending} className={chip(p.allProjects)} onClick={() => update.mutate({ allProjects: !p.allProjects })}>
          {t('peers.allProjects')}
        </button>
        {!p.allProjects &&
          local.map((x) => (
            <button
              key={x.name}
              type="button"
              aria-pressed={shared.includes(x.name)}
              data-share-project={x.name}
              disabled={update.isPending}
              className={chip(shared.includes(x.name))}
              onClick={() => toggle(x.name)}
            >
              {x.displayName || x.name}
            </button>
          ))}
      </div>
      {!p.allProjects && shared.length === 0 && <p className="pl-7 text-caption text-subtle">{t('peers.nothingShared')}</p>}
      <div className="flex min-w-0 items-center gap-3 pl-7">
        <span className="text-caption text-subtle">{t('peers.slots')}</span>
        <div className="w-40">
          <Select aria-label={t('peers.slots')} value={String(p.slots)} onChange={(v) => update.mutate({ slots: Number(v) })}>
            <SelectOption value="-1">{t('peers.slotsDefault', { n: slots })}</SelectOption>
            {[0, 1, 2, 3, 4, 6, 8, 12, 16].map((n) => (
              <SelectOption key={n} value={String(n)}>
                {String(n)}
              </SelectOption>
            ))}
          </Select>
        </div>
      </div>
    </li>
  );
}

function ShareJoin() {
  const t = useT();
  const share = useShare();
  const queryClient = useQueryClient();
  const [link, setLink] = useState('');
  const join = useMutation({
    mutationFn: () => api.addShareRemote(link.trim()),
    onSuccess: (r) => {
      setLink('');
      toast.success(t('peers.joinedToast', { name: r.name }));
      void queryClient.invalidateQueries({ queryKey: ['share'] });
      void queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const forget = useMutation({
    mutationFn: (id: string) => api.removeShareRemote(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['share'] });
      void queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const remotes = share.data?.remotes ?? [];
  return (
    <SettingRow label={t('peers.joinLabel')} description={t('peers.joinDescription')}>
      <div className="grid gap-3" data-share-join>
        <form
          className="flex min-w-0 gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (link.trim()) join.mutate();
          }}
        >
          <Input className="min-w-0 flex-1" data-share-link-input aria-label={t('peers.joinLabel')} placeholder={t('peers.linkPlaceholder')} value={link} onChange={(e) => setLink(e.target.value)} />
          <Button size="sm" variant="primary" type="submit" data-share-join-button disabled={!link.trim() || join.isPending} className="h-9 shrink-0">
            {t('peers.join')}
          </Button>
        </form>
        {remotes.length === 0 ? (
          <SettingNote>{t('peers.noRemotes')}</SettingNote>
        ) : (
          <ul className="grid gap-2" data-share-remotes>
            {remotes.map((r) => (
              <li key={r.id} className="flex min-w-0 items-center gap-3 rounded-xl border border-line px-3 py-2.5" data-share-remote={r.id}>
                <Monitor className="size-4 shrink-0 text-subtle" />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-ui font-medium text-primary">{r.name}</p>
                  <p className="break-words text-caption text-subtle [overflow-wrap:anywhere]">
                    {t('peers.remoteProjects', { count: r.projects })} · <span className="font-mono">{r.url}</span>
                  </p>
                  {r.error && <SettingNote tone="error">{r.error}</SettingNote>}
                </div>
                <Button size="sm" variant="ghost" className="shrink-0" disabled={forget.isPending} onClick={() => forget.mutate(r.id)}>
                  {t('peers.forget')}
                </Button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </SettingRow>
  );
}
