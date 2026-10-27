import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowUpCircle, Ban, Info, LoaderCircle, Play, Plus, Stethoscope, Trash2 } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatDateTime, t, useT } from '../lib/i18n';
import type { SettingGroup } from '../lib/settingsSearch';
import { errorMessage, timeAgo } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input, Label, Textarea } from './ui/input';
import { SettingNote } from './ui/settings';
import { Switch } from './ui/switch';

// Where agents run when they don't run on this machine: Kubernetes clusters
// (internal/daemon/clusters.go, internal/k8s) and the OCI registries their images
// come from. A cluster is added with the kubeconfig you already use for kubectl;
// the daemon keeps only the ServiceAccount's token it makes from it.
//
// The command line is the normal way in for a cluster, because it reads the
// kubeconfig from a file and can pipe a registry token in (`agentbox cluster add`,
// `agentbox registry add`). Here the kubeconfig is pasted and the token typed,
// which is the same thing for one that isn't on this machine.

export function clusterGroups(): SettingGroup[] {
  return [
    {
      id: 'clusters',
      title: t('settings.clusters.title'),
      description: t('settings.clusters.description'),
      entries: [
        {
          id: 'clusters-list',
          label: t('settings.clusters.listLabel'),
          keywords: 'kubernetes k8s cluster clusters pods namespace capacity doctor token rotate',
          render: () => <ClusterList />,
        },
        {
          id: 'clusters-add',
          label: t('settings.clusters.addLabel'),
          keywords: 'add cluster kubeconfig k3d namespace storage class image registry',
          advanced: true,
          render: () => <ClusterForm />,
        },
      ],
    },
    {
      id: 'registries',
      title: t('settings.registries.title'),
      description: t('settings.registries.description'),
      entries: [
        {
          id: 'registries-list',
          label: t('settings.registries.listLabel'),
          keywords: 'registry registries forgejo ghcr docker hub image pull secret login',
          render: () => <RegistryList />,
        },
        {
          id: 'registries-add',
          label: t('settings.registries.addLabel'),
          keywords: 'add registry url username token public insecure',
          advanced: true,
          render: () => <RegistryForm />,
        },
      ],
    },
  ];
}

// useClusters is the clusters the daemon knows, and the query key everything
// here invalidates: a doctor and a remove both change what the list says.
function useClusters() {
  return useQuery({ queryKey: ['clusters'], queryFn: api.clusters });
}

function ClusterList() {
  const t = useT();
  const queryClient = useQueryClient();
  const clusters = useClusters();
  const [checking, setChecking] = useState<string>('');
  const [remove, setRemove] = useState<T.Cluster | null>(null);
  const doctor = useMutation({
    mutationFn: (name: string) => api.doctorCluster(name),
    onMutate: (name) => setChecking(name),
    onSuccess: (report) => {
      toast(report.ok ? t('settings.clusters.doctorOK') : t('settings.clusters.doctorFailed'));
      void queryClient.invalidateQueries({ queryKey: ['clusters'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
    onSettled: () => setChecking(''),
  });
  if (clusters.isPending) {
    return <SettingNote>{t('common.loading')}</SettingNote>;
  }
  if (clusters.error) {
    return <Notice>{errorMessage(clusters.error)}</Notice>;
  }
  const list = clusters.data ?? [];
  return (
    <>
      {list.length === 0 ? (
        <SettingNote>{t('settings.clusters.none')}</SettingNote>
      ) : (
        <ul className="grid gap-3">
          {list.map((cluster) => (
            <ClusterCard
              key={cluster.name}
              cluster={cluster}
              others={list.filter((other) => other.name !== cluster.name && !other.draining)}
              checking={doctor.isPending && checking === cluster.name}
              onDoctor={() => doctor.mutate(cluster.name)}
              onRemove={() => setRemove(cluster)}
            />
          ))}
        </ul>
      )}
      <ConfirmDialog
        open={remove !== null}
        onOpenChange={(open) => !open && setRemove(null)}
        title={t('settings.clusters.removeTitle', { name: remove?.name ?? '' })}
        description={t('settings.clusters.removeDescription')}
        confirmLabel={t('settings.clusters.removeConfirm')}
        destructive
        onConfirm={async () => {
          if (!remove) return;
          await api.removeCluster(remove.name);
          toast(t('settings.clusters.removed', { name: remove.name }));
          await queryClient.invalidateQueries({ queryKey: ['clusters'] });
        }}
      />
    </>
  );
}

// ClusterCard is one cluster: what it runs, how much room it has, and the four
// things that change it: draining it, upgrading its image, reading what it
// holds now, and forgetting it.
function ClusterCard({
  cluster,
  others,
  checking,
  onDoctor,
  onRemove,
}: {
  cluster: T.Cluster;
  // others are the clusters a drain could move the agents to: the ones that are
  // not this one and are not draining themselves.
  others: T.Cluster[];
  checking: boolean;
  onDoctor: () => void;
  onRemove: () => void;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const [details, setDetails] = useState(false);
  const [draining, setDraining] = useState(false);
  const [upgrading, setUpgrading] = useState(false);
  // What a drain does with the agents it has. Neither field is the daemon's
  // stop, keepRunning leaves them, and moveTo sends them to another cluster.
  const [drainChoice, setDrainChoice] = useState<'stop' | 'keep' | 'move'>('stop');
  const [moveTo, setMoveTo] = useState('');
  const [digest, setDigest] = useState('');
  const refresh = () =>
    void Promise.all([
      queryClient.invalidateQueries({ queryKey: ['clusters'] }),
      // A drain and an upgrade are jobs: the sidebar and Jobs read them.
      queryClient.invalidateQueries({ queryKey: ['jobs'] }),
    ]);
  const drain = useMutation({
    mutationFn: (req: T.DrainClusterRequest) => api.drainCluster(cluster.name, req),
    onSuccess: () => {
      toast(t('settings.clusters.drainStarted', { name: cluster.name }));
      refresh();
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const undrain = useMutation({
    mutationFn: () => api.undrainCluster(cluster.name),
    onSuccess: () => {
      toast(t('settings.clusters.undrained', { name: cluster.name }));
      refresh();
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const upgrade = useMutation({
    mutationFn: (req: T.UpgradeClusterRequest) => api.upgradeCluster(cluster.name, req),
    onSuccess: () => {
      toast(t('settings.clusters.upgradeStarted', { name: cluster.name }));
      setUpgrading(false);
      setDigest('');
      refresh();
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <li className="grid gap-1.5 rounded-md border border-line-faint p-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-mono text-[13px] text-primary">{cluster.name}</span>
        <Badge>{cluster.namespace}</Badge>
        {cluster.registry && <Badge>{cluster.registry}</Badge>}
        {cluster.tailnet !== 'none' && <Badge>{t('settings.clusters.tailnetBadge', { mode: cluster.tailnet })}</Badge>}
        {cluster.draining && <Badge variant="warning">{t('settings.clusters.drainingBadge')}</Badge>}
        <span className="ml-auto flex flex-wrap items-center justify-end gap-1">
          <Button variant="ghost" size="sm" data-cluster-details={cluster.name} onClick={() => setDetails(true)}>
            <Info />
            {t('settings.clusters.details')}
          </Button>
          <Button variant="ghost" size="sm" disabled={checking} onClick={onDoctor}>
            {checking ? <LoaderCircle className="animate-spin" /> : <Stethoscope />}
            {t('settings.clusters.doctor')}
          </Button>
          {cluster.draining ? (
            <Button variant="ghost" size="sm" disabled={undrain.isPending} onClick={() => undrain.mutate()}>
              {undrain.isPending ? <LoaderCircle className="animate-spin" /> : <Play />}
              {t('settings.clusters.undrain')}
            </Button>
          ) : (
            <Button variant="ghost" size="sm" onClick={() => setDraining(true)}>
              <Ban />
              {t('settings.clusters.drain')}
            </Button>
          )}
          {cluster.image && (
            <Button variant="ghost" size="sm" onClick={() => setUpgrading(true)}>
              <ArrowUpCircle />
              {t('settings.clusters.upgrade')}
            </Button>
          )}
          <Button variant="ghost" size="sm" onClick={onRemove}>
            <Trash2 />
            {t('common.remove')}
          </Button>
        </span>
      </div>
      <p className="break-words text-[12px] leading-relaxed text-subtle">
        {cluster.image ? t('settings.clusters.imageLine', { image: cluster.image }) : t('settings.clusters.noImage')}
        {cluster.storageClass ? ` · ${t('settings.clusters.storageClassLine', { class: cluster.storageClass })}` : ''}
        {cluster.previousImage ? ` · ${t('settings.clusters.previousImage', { image: cluster.previousImage })}` : ''}
        {cluster.lastSeen ? ` · ${t('settings.clusters.lastSeen', { when: formatDateTime(new Date(cluster.lastSeen)) })}` : ''}
      </p>
      <p className="break-words text-[12px] leading-relaxed text-secondary">{capacityLine(cluster)}</p>
      {cluster.capacity?.error && <Notice>{cluster.capacity.error}</Notice>}
      {cluster.lastError && <Notice>{cluster.lastError}</Notice>}

      <ConfirmDialog
        open={draining}
        onOpenChange={setDraining}
        title={t('settings.clusters.drainTitle', { name: cluster.name })}
        description={t('settings.clusters.drainDescription')}
        confirmLabel={t('settings.clusters.drainConfirm')}
        destructive
        onConfirm={async () => {
          if (drainChoice === 'move' && !moveTo) throw new Error(t('settings.clusters.drainPickTarget'));
          await drain.mutateAsync(drainChoice === 'keep' ? { keepRunning: true } : drainChoice === 'move' ? { moveTo } : {});
        }}
      >
        <div className="grid gap-2 text-[13px] text-secondary">
          {(
            [
              ['stop', t('settings.clusters.drainStop')],
              ['keep', t('settings.clusters.drainKeep')],
            ] as const
          ).map(([choice, label]) => (
            <label key={choice} className="flex items-center gap-2">
              <input
                type="radio"
                name={`drain-${cluster.name}`}
                checked={drainChoice === choice}
                onChange={() => setDrainChoice(choice)}
                className="accent-brand-400"
              />
              {label}
            </label>
          ))}
          <label className="flex flex-wrap items-center gap-2">
            <input
              type="radio"
              name={`drain-${cluster.name}`}
              checked={drainChoice === 'move'}
              onChange={() => setDrainChoice('move')}
              className="accent-brand-400"
            />
            {t('settings.clusters.drainMove')}
            <select
              className="h-8 min-w-0 rounded-md border border-line-vivid bg-transparent px-2 text-[13px] text-primary"
              value={moveTo}
              disabled={drainChoice !== 'move' || others.length === 0}
              onChange={(event) => setMoveTo(event.target.value)}
            >
              <option value="">{t('settings.clusters.drainPickTarget')}</option>
              {others.map((other) => (
                <option key={other.name} value={other.name}>
                  {other.name}
                </option>
              ))}
            </select>
          </label>
        </div>
      </ConfirmDialog>

      <Dialog open={upgrading} onOpenChange={setUpgrading}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('settings.clusters.upgradeTitle', { name: cluster.name })}</DialogTitle>
            <DialogDescription>{t('settings.clusters.upgradeDescription')}</DialogDescription>
          </DialogHeader>
          <Field label={t('settings.clusters.digest')} htmlFor={`cluster-digest-${cluster.name}`} hint={t('settings.clusters.digestHint')}>
            <Input
              id={`cluster-digest-${cluster.name}`}
              value={digest}
              onChange={(event) => setDigest(event.target.value)}
              placeholder="sha256:…"
              spellCheck={false}
              className="font-mono text-[12px]"
            />
          </Field>
          {cluster.previousImage && <p className="break-words text-[12px] leading-relaxed text-subtle">{t('settings.clusters.previousImage', { image: cluster.previousImage })}</p>}
          {upgrade.error && <Notice>{errorMessage(upgrade.error)}</Notice>}
          <DialogFooter>
            <Button variant="ghost" onClick={() => setUpgrading(false)}>
              {t('common.cancel')}
            </Button>
            {cluster.previousImage && (
              <Button variant="secondary" disabled={upgrade.isPending} onClick={() => upgrade.mutate({ rollback: true })}>
                {upgrade.isPending && <LoaderCircle className="animate-spin" />}
                {t('settings.clusters.rollback')}
              </Button>
            )}
            <Button disabled={digest.trim() === '' || upgrade.isPending} onClick={() => upgrade.mutate({ digest: digest.trim() })}>
              {upgrade.isPending && <LoaderCircle className="animate-spin" />}
              {t('settings.clusters.upgrade')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ClusterDetailDialog cluster={cluster} open={details} onOpenChange={setDetails} />
    </li>
  );
}

// capacityLine is how full a cluster is, in one line: what runs on it, what is
// free, and what is left to place agents with.
function capacityLine(cluster: T.Cluster): string {
  const capacity = cluster.capacity;
  if (!capacity) return t('settings.clusters.capacityUnknown');
  return [
    cluster.maxAgents > 0
      ? t('settings.clusters.capacityOf', { running: capacity.running, max: cluster.maxAgents })
      : t('settings.clusters.capacityRunning', { running: capacity.running }),
    capacity.agents < 0 ? t('settings.clusters.capacityUnlimited') : t('settings.clusters.capacityFree', { agents: capacity.agents }),
    capacity.cpu ? t('settings.clusters.capacityCPU', { cpu: capacity.cpu }) : '',
    capacity.memory ? t('settings.clusters.capacityMemory', { memory: capacity.memory }) : '',
    t('settings.clusters.capacityAt', { when: timeAgo(capacity.at) }),
  ]
    .filter(Boolean)
    .join(' · ');
}

// ClusterDetailDialog is what a cluster holds right now: its pods, the claims
// their work lives on, the agents waiting for room, and the cluster's own
// recent events.
function ClusterDetailDialog({ cluster, open, onOpenChange }: { cluster: T.Cluster; open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT();
  const detail = useQuery({
    queryKey: ['clusterDetail', cluster.name],
    queryFn: () => api.clusterDetail(cluster.name),
    enabled: open,
  });
  const data = detail.data;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t('settings.clusters.detailTitle', { name: cluster.name })}</DialogTitle>
          <DialogDescription>{capacityLine(data?.cluster ?? cluster)}</DialogDescription>
        </DialogHeader>
        {detail.isPending && <SettingNote>{t('common.loading')}</SettingNote>}
        {detail.error && <Notice>{errorMessage(detail.error)}</Notice>}
        {data?.error && <Notice>{data.error}</Notice>}
        {data && (
          <div className="grid max-h-[60vh] min-w-0 gap-5 overflow-y-auto pr-1">
            <DetailSection title={t('settings.clusters.pods')} empty={t('settings.clusters.noPods')} count={data.pods?.length ?? 0}>
              {(data.pods ?? []).map((pod) => (
                <li key={pod.name} className="grid min-w-0 gap-1 rounded-md border border-line-faint p-2">
                  <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                    <span className="min-w-0 break-all font-mono text-[11.5px] text-primary">{pod.name}</span>
                    {pod.project && pod.agent && <Badge>{`${pod.project}/${pod.agent}`}</Badge>}
                    <Badge variant={pod.phase === 'Running' && pod.ready ? 'success' : pod.phase === 'Failed' ? 'danger' : 'default'}>{pod.phase}</Badge>
                    <Badge>{pod.ready ? t('settings.clusters.ready') : t('settings.clusters.notReady')}</Badge>
                    {pod.restarts ? <Badge variant="warning">{t('settings.clusters.restarts', { count: pod.restarts })}</Badge> : null}
                  </div>
                  <p className="break-words text-[11.5px] leading-relaxed text-subtle">
                    {pod.node ? `${pod.node} · ` : ''}
                    {pod.image ? shortImage(pod.image) : '—'}
                    {pod.createdAt ? ` · ${timeAgo(pod.createdAt)}` : ''}
                  </p>
                  {(pod.reason || pod.message) && (
                    <p className="break-words text-[11.5px] leading-relaxed text-amber-300/90">{[pod.reason, pod.message].filter(Boolean).join(': ')}</p>
                  )}
                </li>
              ))}
            </DetailSection>

            <DetailSection title={t('settings.clusters.claims')} empty={t('settings.clusters.noClaims')} count={data.claims?.length ?? 0}>
              {(data.claims ?? []).map((claim) => (
                <li key={claim.name} className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1 rounded-md border border-line-faint p-2 text-[11.5px]">
                  <span className="min-w-0 break-all font-mono text-primary">{claim.name}</span>
                  {claim.project && claim.agent && <span className="text-subtle">{`${claim.project}/${claim.agent}`}</span>}
                  <span className="ml-auto font-mono text-secondary">{claim.storage || '—'}</span>
                </li>
              ))}
            </DetailSection>

            <DetailSection title={t('settings.clusters.queue')} empty={t('settings.clusters.noQueue')} count={data.queue?.length ?? 0}>
              {(data.queue ?? []).map((queued) => (
                <li key={`${queued.project}/${queued.agent}`} className="grid min-w-0 gap-0.5 rounded-md border border-line-faint p-2 text-[11.5px]">
                  <span className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
                    <span className="text-primary">{`${queued.project}/${queued.agent}`}</span>
                    <Badge>{t('settings.clusters.queuePlace', { position: queued.position })}</Badge>
                  </span>
                  {queued.reason && <span className="break-words leading-relaxed text-subtle">{queued.reason}</span>}
                </li>
              ))}
            </DetailSection>

            <DetailSection title={t('settings.clusters.events')} empty={t('settings.clusters.noEvents')} count={data.events?.length ?? 0}>
              {(data.events ?? []).map((event, index) => (
                <li key={`${event.object}-${event.reason}-${index}`} className="grid min-w-0 gap-0.5 rounded-md border border-line-faint p-2 text-[11.5px]">
                  <span className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
                    <Badge variant={event.type === 'Warning' ? 'warning' : 'default'}>{event.type}</Badge>
                    <span className="text-primary">{event.reason}</span>
                    <span className="min-w-0 break-all font-mono text-[10.5px] text-subtle">{event.object}</span>
                    {(event.count ?? 0) > 1 && <span className="text-faint">×{event.count}</span>}
                    <span className="ml-auto text-faint">{timeAgo(event.at)}</span>
                  </span>
                  {event.message && <span className="break-words leading-relaxed text-subtle">{event.message}</span>}
                </li>
              ))}
            </DetailSection>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

// DetailSection is one titled list of the detail dialog, with its own empty
// state: a list the cluster has nothing for says so rather than vanishing.
function DetailSection({ title, empty, count, children }: { title: string; empty: string; count: number; children: ReactNode }) {
  return (
    <section className="grid min-w-0 gap-1.5">
      <h3 className="text-[12px] font-medium uppercase tracking-[0.06em] text-faint">{title}</h3>
      {count === 0 ? <p className="text-[12px] leading-relaxed text-subtle">{empty}</p> : <ul className="grid min-w-0 gap-1.5">{children}</ul>}
    </section>
  );
}

// shortImage is the image without its registry prefix: the page has the full
// reference in its title, and the tag or digest is what changes.
function shortImage(image: string): string {
  return image.split('/').pop() ?? image;
}

function ClusterForm() {
  const t = useT();
  const queryClient = useQueryClient();
  const registries = useQuery({ queryKey: ['registries'], queryFn: api.registries });
  const [name, setName] = useState('');
  const [namespace, setNamespace] = useState('agentbox');
  const [image, setImage] = useState('');
  const [registry, setRegistry] = useState('');
  const [storageClass, setStorageClass] = useState('');
  const [kubeconfig, setKubeconfig] = useState('');
  const add = useMutation({
    mutationFn: () =>
      api.addCluster({
        name: name.trim(),
        namespace: namespace.trim() || undefined,
        image: image.trim() || undefined,
        registry: registry || undefined,
        storageClass: storageClass.trim() || undefined,
        tailnet: 'none',
        kubeconfig,
      }),
    onSuccess: async (cluster) => {
      toast(t('settings.clusters.added', { name: cluster.name }));
      setName('');
      setImage('');
      setKubeconfig('');
      await queryClient.invalidateQueries({ queryKey: ['clusters'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const ready = name.trim() !== '' && kubeconfig.trim() !== '';
  return (
    <form
      className="grid gap-2.5"
      onSubmit={(event) => {
        event.preventDefault();
        if (ready) add.mutate();
      }}
    >
      <p className="text-[13px] leading-relaxed text-muted">{t('settings.clusters.addIntro')}</p>
      <div className="flex flex-wrap gap-2">
        <Field label={t('settings.clusters.name')} htmlFor="cluster-name">
          <Input id="cluster-name" value={name} onChange={(event) => setName(event.target.value)} placeholder="k3d" className="w-32 font-mono" />
        </Field>
        <Field label={t('settings.clusters.namespace')} htmlFor="cluster-namespace">
          <Input
            id="cluster-namespace"
            value={namespace}
            onChange={(event) => setNamespace(event.target.value)}
            placeholder="agentbox"
            className="w-36 font-mono"
          />
        </Field>
        <Field label={t('settings.clusters.image')} htmlFor="cluster-image">
          <Input
            id="cluster-image"
            value={image}
            onChange={(event) => setImage(event.target.value)}
            placeholder="agentbox-agent:dev"
            className="w-52 font-mono"
          />
        </Field>
        <Field label={t('settings.clusters.registry')} htmlFor="cluster-registry">
          <select
            id="cluster-registry"
            className="h-8 rounded-md border border-line-vivid bg-transparent px-2 text-[13px] text-primary"
            value={registry}
            onChange={(event) => setRegistry(event.target.value)}
          >
            <option value="">{t('settings.clusters.registryNone')}</option>
            {(registries.data ?? []).map((reg) => (
              <option key={reg.name} value={reg.name}>
                {reg.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t('settings.clusters.storageClass')} htmlFor="cluster-storage">
          <Input
            id="cluster-storage"
            value={storageClass}
            onChange={(event) => setStorageClass(event.target.value)}
            placeholder="local-path"
            className="w-36 font-mono"
          />
        </Field>
      </div>
      <Field label={t('settings.clusters.kubeconfig')} htmlFor="cluster-kubeconfig" hint={t('settings.clusters.kubeconfigHint')}>
        <Textarea
          id="cluster-kubeconfig"
          value={kubeconfig}
          onChange={(event) => setKubeconfig(event.target.value)}
          rows={6}
          spellCheck={false}
          placeholder={'apiVersion: v1\nkind: Config\nclusters: …'}
          className="font-mono text-[12px]"
        />
      </Field>
      <div className="flex items-center gap-2">
        <Button type="submit" variant="secondary" disabled={!ready || add.isPending}>
          {add.isPending ? <LoaderCircle className="animate-spin" /> : <Plus />}
          {t('settings.clusters.add')}
        </Button>
        <SettingNote>{t('settings.clusters.addSlow')}</SettingNote>
      </div>
      {add.error && <Notice>{errorMessage(add.error)}</Notice>}
    </form>
  );
}

function RegistryList() {
  const t = useT();
  const queryClient = useQueryClient();
  const registries = useQuery({ queryKey: ['registries'], queryFn: api.registries });
  const [checking, setChecking] = useState<string>('');
  const [remove, setRemove] = useState<T.Registry | null>(null);
  const test = useMutation({
    mutationFn: (name: string) => api.testRegistry(name),
    onMutate: (name) => setChecking(name),
    onSuccess: (result) => {
      if (result.ok) toast(result.detail);
      else toast.error(result.detail);
      void queryClient.invalidateQueries({ queryKey: ['registries'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
    onSettled: () => setChecking(''),
  });
  if (registries.isPending) {
    return <SettingNote>{t('common.loading')}</SettingNote>;
  }
  if (registries.error) {
    return <Notice>{errorMessage(registries.error)}</Notice>;
  }
  const list = registries.data ?? [];
  return (
    <>
      {list.length === 0 ? (
        <SettingNote>{t('settings.registries.none')}</SettingNote>
      ) : (
        <ul className="grid gap-3">
          {list.map((reg) => (
            <li key={reg.name} className="grid gap-1.5 rounded-md border border-line-faint p-3">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-[13px] text-primary">{reg.name}</span>
                <span className="font-mono text-[12px] text-subtle">{reg.url}</span>
                {reg.public && <Badge>{t('settings.registries.public')}</Badge>}
                <Badge>
                  {reg.hasCredential ? t('settings.registries.withLogin') : t('settings.registries.noLogin')}
                </Badge>
                <span className="ml-auto flex items-center gap-1">
                  <Button variant="ghost" size="sm" disabled={test.isPending && checking === reg.name} onClick={() => test.mutate(reg.name)}>
                    {test.isPending && checking === reg.name ? <LoaderCircle className="animate-spin" /> : <Stethoscope />}
                    {t('settings.registries.test')}
                  </Button>
                  <Button variant="ghost" size="sm" onClick={() => setRemove(reg)}>
                    <Trash2 />
                    {t('common.remove')}
                  </Button>
                </span>
              </div>
              {reg.lastError && <Notice>{reg.lastError}</Notice>}
            </li>
          ))}
        </ul>
      )}
      <ConfirmDialog
        open={remove !== null}
        onOpenChange={(open) => !open && setRemove(null)}
        title={t('settings.registries.removeTitle', { name: remove?.name ?? '' })}
        description={t('settings.registries.removeDescription')}
        confirmLabel={t('settings.registries.removeConfirm')}
        destructive
        onConfirm={async () => {
          if (!remove) return;
          await api.removeRegistry(remove.name);
          toast(t('settings.registries.removed', { name: remove.name }));
          await Promise.all([
            queryClient.invalidateQueries({ queryKey: ['registries'] }),
            queryClient.invalidateQueries({ queryKey: ['clusters'] }),
          ]);
        }}
      />
    </>
  );
}

function RegistryForm() {
  const t = useT();
  const queryClient = useQueryClient();
  const [name, setName] = useState('');
  const [url, setURL] = useState('');
  const [username, setUsername] = useState('');
  const [token, setToken] = useState('');
  const [publicRegistry, setPublic] = useState(false);
  const [insecure, setInsecure] = useState(false);
  const add = useMutation({
    mutationFn: () =>
      api.addRegistry({
        name: name.trim(),
        url: url.trim(),
        username: username.trim() || undefined,
        token: token || undefined,
        public: publicRegistry,
        insecure,
      }),
    onSuccess: async (reg) => {
      toast(t('settings.registries.added', { name: reg.name }));
      setName('');
      setURL('');
      setUsername('');
      setToken('');
      await queryClient.invalidateQueries({ queryKey: ['registries'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const ready = name.trim() !== '' && url.trim() !== '';
  return (
    <form
      className="grid gap-2.5"
      onSubmit={(event) => {
        event.preventDefault();
        if (ready) add.mutate();
      }}
    >
      <p className="text-[13px] leading-relaxed text-muted">{t('settings.registries.addIntro')}</p>
      <div className="flex flex-wrap items-end gap-2">
        <Field label={t('settings.registries.name')} htmlFor="registry-name">
          <Input id="registry-name" value={name} onChange={(event) => setName(event.target.value)} placeholder="forgejo" className="w-32 font-mono" />
        </Field>
        <div className="grid min-w-56 flex-1 gap-1.5">
          <Label htmlFor="registry-url">{t('settings.registries.url')}</Label>
          <Input
            id="registry-url"
            value={url}
            onChange={(event) => setURL(event.target.value)}
            placeholder="hub.example.com/h4ad"
            className="font-mono"
          />
        </div>
        <Field label={t('settings.registries.username')} htmlFor="registry-username">
          <Input
            id="registry-username"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
            className="w-32 font-mono"
            autoComplete="off"
          />
        </Field>
        <div className="grid min-w-48 flex-1 gap-1.5">
          <Label htmlFor="registry-token">{t('settings.registries.token')}</Label>
          <Input
            id="registry-token"
            type="password"
            value={token}
            onChange={(event) => setToken(event.target.value)}
            autoComplete="off"
            spellCheck={false}
            className="font-mono"
          />
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-5">
        <label className="flex items-center gap-2 text-[13px] text-muted">
          <Switch checked={publicRegistry} onCheckedChange={setPublic} />
          {t('settings.registries.publicSwitch')}
        </label>
        <label className="flex items-center gap-2 text-[13px] text-muted">
          <Switch checked={insecure} onCheckedChange={setInsecure} />
          {t('settings.registries.insecureSwitch')}
        </label>
        <Button type="submit" variant="secondary" disabled={!ready || add.isPending}>
          {add.isPending ? <LoaderCircle className="animate-spin" /> : <Plus />}
          {t('settings.registries.add')}
        </Button>
      </div>
      <SettingNote>{t('settings.registries.tokenNote')}</SettingNote>
      {add.error && <Notice>{errorMessage(add.error)}</Notice>}
    </form>
  );
}
