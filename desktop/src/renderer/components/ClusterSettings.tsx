import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { LoaderCircle, Plus, Stethoscope, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatDateTime, t, useT } from '../lib/i18n';
import type { SettingGroup } from '../lib/settingsSearch';
import { errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Notice } from './ui/card';
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
            <li key={cluster.name} className="grid gap-1.5 rounded-md border border-line-faint p-3">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-[13px] text-primary">{cluster.name}</span>
                <Badge>{cluster.namespace}</Badge>
                {cluster.registry && <Badge>{cluster.registry}</Badge>}
                {cluster.tailnet !== 'none' && <Badge>{t('settings.clusters.tailnetBadge', { mode: cluster.tailnet })}</Badge>}
                <span className="ml-auto flex items-center gap-1">
                  <Button variant="ghost" size="sm" disabled={doctor.isPending && checking === cluster.name} onClick={() => doctor.mutate(cluster.name)}>
                    {doctor.isPending && checking === cluster.name ? <LoaderCircle className="animate-spin" /> : <Stethoscope />}
                    {t('settings.clusters.doctor')}
                  </Button>
                  <Button variant="ghost" size="sm" onClick={() => setRemove(cluster)}>
                    <Trash2 />
                    {t('common.remove')}
                  </Button>
                </span>
              </div>
              <p className="text-[12px] leading-relaxed text-subtle">
                {cluster.image ? t('settings.clusters.imageLine', { image: cluster.image }) : t('settings.clusters.noImage')}
                {cluster.storageClass ? ` · ${t('settings.clusters.storageClassLine', { class: cluster.storageClass })}` : ''}
                {cluster.lastSeen ? ` · ${t('settings.clusters.lastSeen', { when: formatDateTime(new Date(cluster.lastSeen)) })}` : ''}
              </p>
              {cluster.lastError && <Notice>{cluster.lastError}</Notice>}
            </li>
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
