import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Copy, Link2, Monitor, RefreshCw, Share2 } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatTokens } from '../lib/chat';
import { maxShareSlots, parseSlots } from '../lib/shareSlots';
import { formatDate, formatDateTime, t, useT } from '../lib/i18n';
import type { SettingGroup } from '../lib/settingsSearch';
import { cn, errorMessage } from '../lib/utils';
import { SyncDialog } from './SyncDialog';
import { Button } from './ui/button';
import { Field, Input } from './ui/input';
import { Select, SelectOption } from './ui/select';
import { Switch } from './ui/switch';
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
  // Pairing goes through the phone page: off, it says so in the app's words.
  const lanOn = !!useQuery({ queryKey: ['lan'], queryFn: api.lan }).data?.enabled;
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
  // The default slots save once typing stops, or on leaving the field or
  // Enter; emptied, or anything but 0 to 64, saves nothing and goes back to
  // what is saved on leaving.
  const [slots, setSlots] = useState<string | null>(null);
  const slotsTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(slotsTimer.current), []);
  const saveSlots = (text: string): boolean => {
    clearTimeout(slotsTimer.current);
    const n = parseSlots(text);
    if (n === null || n === st?.slots) return false;
    save.mutate({ slots: n }, { onSettled: () => setSlots((cur) => (cur === text ? null : cur)) });
    return true;
  };
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
              max={maxShareSlots}
              value={slots ?? String(st.slots)}
              onChange={(e) => {
                const text = e.target.value;
                setSlots(text);
                clearTimeout(slotsTimer.current);
                slotsTimer.current = setTimeout(() => saveSlots(text), 800);
              }}
              onBlur={() => {
                if (slots === null || !saveSlots(slots)) setSlots(null);
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') e.currentTarget.blur();
              }}
            />
          </Field>
        </div>
        {!st.ready && <SettingNote tone="muted">{lanOn ? t('peers.noAddress', { error: st.error ?? '' }) : t('peers.phonesOff')}</SettingNote>}
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
          <p className="break-words text-caption text-subtle [overflow-wrap:anywhere]" data-share-usage>
            {t('peers.usage', { tokens: formatTokens(p.usage?.tokens ?? 0), minutes: p.usage?.agentMinutes ?? 0 })}
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
        <span className="text-caption text-subtle">{t('peers.access')}</span>
        <div className="w-64" data-share-access={p.access || 'full'}>
          <Select aria-label={t('peers.access')} value={p.access || 'full'} onChange={(v) => update.mutate({ access: v })}>
            <SelectOption value="view">{t('peers.access.view')}</SelectOption>
            <SelectOption value="chat">{t('peers.access.chat')}</SelectOption>
            <SelectOption value="full">{t('peers.access.full')}</SelectOption>
          </Select>
        </div>
      </div>
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
      <PeerActivity peer={p} />
      {/* Agents its chat messages ask to run on its own computer
          (internal/daemon/sharemachines.go): on its accounts, not your slots. */}
      <div className="flex min-w-0 items-start gap-3 pl-7" data-share-own-machine>
        <Switch
          id={`share-own-machine-${p.id}`}
          className="mt-0.5"
          checked={p.ownMachine}
          disabled={update.isPending}
          onCheckedChange={(on) => update.mutate({ ownMachine: on })}
        />
        <label htmlFor={`share-own-machine-${p.id}`} className="grid min-w-0 gap-0.5">
          <span className="text-caption text-secondary">{t('peers.ownMachine')}</span>
          <span className="break-words text-caption text-subtle [overflow-wrap:anywhere]">
            {p.ownMachine ? (p.machine ? t('peers.ownMachineOn', { name: p.name }) : t('peers.ownMachineAway', { name: p.name })) : t('peers.ownMachineOff')}
          </span>
        </label>
      </div>
      {/* Syncing (internal/daemon/sync.go): it copies the data of the projects
          it reaches, and merges its own into this AgentBox. */}
      <div className="flex min-w-0 items-start gap-3 pl-7" data-share-sync>
        <Switch id={`share-sync-${p.id}`} className="mt-0.5" checked={p.sync} disabled={update.isPending} onCheckedChange={(on) => update.mutate({ sync: on })} />
        <label htmlFor={`share-sync-${p.id}`} className="grid min-w-0 gap-0.5">
          <span className="text-caption text-secondary">{t('peers.sync.allow')}</span>
          <span className="break-words text-caption text-subtle [overflow-wrap:anywhere]">{p.sync ? t('peers.sync.allowOn', { name: p.name }) : t('peers.sync.allowOff')}</span>
        </label>
      </div>
    </li>
  );
}

// PeerActivity is what a paired AgentBox did here, the newest first, with
// when (internal/daemon/shareactivity.go).
function PeerActivity({ peer }: { peer: T.SharePeer }) {
  const t = useT();
  const [limit, setLimit] = useState(8);
  const acts = useQuery({ queryKey: ['shareActivity', peer.id], queryFn: () => api.sharePeerActivity(peer.id, 100) });
  const all = acts.data ?? [];
  const shown = all.slice(0, limit);
  return (
    <div className="grid min-w-0 gap-1.5 pl-7" data-share-activity={peer.id}>
      <span className="text-caption text-subtle">{t('peers.activity')}</span>
      {all.length === 0 ? (
        <p className="text-caption text-faint">{t('peers.activityEmpty')}</p>
      ) : (
        <ul className="grid min-w-0 gap-1 border-l border-line-faint pl-3">
          {shown.map((a) => (
            <li key={a.id} className="flex min-w-0 items-baseline gap-2 text-caption" data-share-act={a.kind}>
              <span className="w-28 shrink-0 tabular-nums text-faint">{formatDateTime(new Date(a.at))}</span>
              <span className="min-w-0 break-words text-secondary [overflow-wrap:anywhere]">
                {activityText(a)}
                {a.project && <span className="text-subtle"> · {a.project}</span>}
              </span>
            </li>
          ))}
        </ul>
      )}
      {all.length > limit && (
        <div>
          <Button size="sm" variant="ghost" onClick={() => setLimit((n) => n + 20)}>
            {t('peers.activityMore')}
          </Button>
        </div>
      )}
    </div>
  );
}

// activityText says what a peer did, in the app's language.
function activityText(a: T.ShareActivity): string {
  const agent = a.agent ?? '';
  switch (a.kind) {
    case 'create-agent':
      return a.detail === 'chat' ? t('peers.did.create-agent-chat', { agent }) : t('peers.did.create-agent', { agent });
    case 'start-agent':
      return t('peers.did.start-agent', { agent });
    case 'stop-agent':
      return t('peers.did.stop-agent', { agent });
    case 'remove-agent':
      return t('peers.did.remove-agent', { agent });
    case 'message':
      return t('peers.did.message');
    case 'prompt':
      return t('peers.did.prompt', { agent });
    case 'answer':
      return t('peers.did.answer', { agent });
    case 'merge':
      return agent ? t('peers.did.merge', { agent }) : t('peers.did.merge-pr', { number: a.detail ?? '' });
    case 'review':
      return t('peers.did.review', { agent });
    case 'open-desktop':
      return t('peers.did.open-desktop', { agent });
    case 'open-terminal':
      return t('peers.did.open-terminal', { agent });
  }
  return t('peers.did.other', { kind: a.kind });
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
  // What to sync with, once asked: one AgentBox, or all of them ([]).
  const [syncWith, setSyncWith] = useState<string[] | null>(null);
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
          <div className="flex min-w-0 flex-wrap items-center gap-3" data-share-sync-all>
            <SettingNote className="min-w-0 flex-1">{t('peers.sync.help')}</SettingNote>
            {remotes.length > 1 && (
              <Button size="sm" variant="secondary" className="shrink-0" onClick={() => setSyncWith([])}>
                <RefreshCw />
                {t('peers.sync.all')}
              </Button>
            )}
          </div>
        )}
        {remotes.length > 0 && (
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
                  {!r.error && (
                    <p className="break-words text-caption text-subtle [overflow-wrap:anywhere]" data-share-remote-machine={r.machine || undefined}>
                      {r.machine ? t('peers.machineOn', { name: r.name }) : t('peers.machineOff', { name: r.name, why: r.machineError || t('peers.machineConnecting') })}
                    </p>
                  )}
                </div>
                <Button size="sm" variant="secondary" className="shrink-0" data-share-sync-button onClick={() => setSyncWith([r.id])}>
                  <RefreshCw />
                  {t('peers.sync.button')}
                </Button>
                <Button size="sm" variant="ghost" className="shrink-0" disabled={forget.isPending} onClick={() => forget.mutate(r.id)}>
                  {t('peers.forget')}
                </Button>
              </li>
            ))}
          </ul>
        )}
        {syncWith && <SyncDialog remotes={syncWith.length > 0 ? syncWith : undefined} onClose={() => setSyncWith(null)} />}
      </div>
    </SettingRow>
  );
}
