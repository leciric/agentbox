import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Blocks, Check, Copy, KeyRound, LoaderCircle } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatDateTime, t, useT, type MessageKey } from '../lib/i18n';
import type { SettingGroup } from '../lib/settingsSearch';
import { cn, errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input } from './ui/input';
import { SettingNote, SettingRow } from './ui/settings';

// Using AgentBox from another app (Claude, ChatGPT, Poke...) over remote MCP,
// through the hub (internal/daemon, /v1/remote/mcp): the apps paired with
// this computer, and the wizard that pairs a new one.

const clients = ['claude', 'chatgpt', 'poke', 'other'] as const;
type Client = (typeof clients)[number];
const accessLevels = ['read', 'chat', 'full'] as const;
type Access = (typeof accessLevels)[number];

const isClient = (c: string): c is Client => (clients as readonly string[]).includes(c);
const asAccess = (a: string): Access => ((accessLevels as readonly string[]).includes(a) ? (a as Access) : 'read');

export function appsGroups(): SettingGroup[] {
  return [
    {
      id: 'apps',
      title: t('settings.section.apps.title'),
      entries: [
        {
          id: 'apps',
          description: t('settings.section.apps.description'),
          label: t('settings.apps.pairedLabel'),
          keywords: t('settings.apps.keywords'),
          render: () => <PairedApps />,
        },
      ],
    },
  ];
}

function PairedApps() {
  const t = useT();
  const queryClient = useQueryClient();
  const apps = useQuery({ queryKey: ['mcp-apps'], queryFn: api.mcpApps });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  // null: closed; otherwise the wizard, opened on its last step when a new code was made.
  const [wizard, setWizard] = useState<{ key: number; secret?: T.MCPPairingSecret } | null>(null);
  const [revoking, setRevoking] = useState<T.MCPPairing | null>(null);
  const st = apps.data;
  const blocked = st && !st.available && st.reason !== 'offline';
  const refresh = () => void queryClient.invalidateQueries({ queryKey: ['mcp-apps'] });
  const newCode = useMutation({
    mutationFn: (id: string) => api.newMCPAppCode(id),
    onSuccess: (secret) => setWizard({ key: Date.now(), secret }),
    onError: (err) => toast.error(t('settings.apps.wizard.codeFailed'), { description: errorMessage(err) }),
  });
  const names = (list: T.Project[] | undefined, name: string) => list?.find((p) => p.name === name)?.displayName || name;
  const when = (iso?: string) => (iso ? t('settings.apps.lastUsed', { when: formatDateTime(new Date(iso)) }) : t('settings.apps.neverUsed'));

  return (
    <SettingRow
      label={t('settings.apps.pairedLabel')}
      control={
        <Button size="sm" variant="primary" data-apps-pair disabled={!st || !!blocked} onClick={() => setWizard({ key: Date.now() })}>
          <Blocks className="size-3.5" /> {t('settings.apps.pair')}
        </Button>
      }
    >
      <div className="grid gap-3">
        {apps.error && <SettingNote tone="error">{t('settings.apps.loadFailed')}: {errorMessage(apps.error)}</SettingNote>}
        {st && !st.available && (
          <SettingNote tone={st.reason === 'offline' ? 'warning' : 'muted'}>
            {st.reason === 'no-hub'
              ? t('settings.apps.noHub')
              : st.reason === 'old-hub'
                ? t('settings.apps.oldHub', { hub: st.hub ?? '' })
                : st.reason === 'offline'
                  ? t('settings.apps.offline', { hub: st.hub ?? '' })
                  : st.error || t('settings.apps.loadFailed')}
          </SettingNote>
        )}
        {st && st.pairings.length === 0 && st.available && <SettingNote>{t('settings.apps.empty')}</SettingNote>}
        {st && st.pairings.length > 0 && (
          <ul className="grid gap-2" data-apps>
            {st.pairings.map((p) => (
              <li key={p.id} className="grid min-w-0 gap-2 rounded-xl border border-line px-3 py-2.5" data-app={p.id}>
                <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
                  <div className="min-w-0 flex-1 basis-48">
                    <div className="flex min-w-0 items-center gap-2">
                      <p className="min-w-0 truncate text-ui font-medium text-primary">{p.label}</p>
                      <Badge variant={p.access === 'read' ? 'default' : p.access === 'chat' ? 'brand' : 'warning'} className="shrink-0">
                        {t(`settings.apps.access.${asAccess(p.access)}` satisfies MessageKey)}
                      </Badge>
                    </div>
                    <p className="break-words text-caption text-subtle [overflow-wrap:anywhere]">
                      {p.projects.length === 0
                        ? t('settings.apps.allProjects')
                        : p.projects.length <= 3
                          ? p.projects.map((n) => names(projects.data, n)).join(', ')
                          : t('settings.apps.projectCount', { n: p.projects.length })}
                      {' · '}
                      {p.apps && p.apps.length > 0 ? t('settings.apps.signedIn', { apps: p.apps.join(', ') }) : t('settings.apps.notSignedIn')}
                      {' · '}
                      {when(p.lastUsedAt)}
                    </p>
                  </div>
                  <div className="flex shrink-0 gap-2">
                    <Button
                      size="sm"
                      variant="secondary"
                      title={t('settings.apps.newCodeHint')}
                      disabled={!st.available || newCode.isPending}
                      onClick={() => newCode.mutate(p.id)}
                    >
                      <KeyRound className="size-3.5" /> {t('settings.apps.newCode')}
                    </Button>
                    <Button size="sm" variant="ghost" onClick={() => setRevoking(p)}>
                      {t('settings.apps.revoke')}
                    </Button>
                  </div>
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>
      {wizard && (
        <AppWizard
          key={wizard.key}
          secret={wizard.secret}
          projects={projects.data ?? []}
          onClose={() => {
            setWizard(null);
            refresh();
          }}
        />
      )}
      <ConfirmDialog
        open={!!revoking}
        onOpenChange={(o) => !o && setRevoking(null)}
        title={t('settings.apps.revokeTitle', { name: revoking?.label ?? '' })}
        description={t('settings.apps.revokeBody')}
        confirmLabel={t('settings.apps.revoke')}
        destructive
        onConfirm={async () => {
          const p = revoking;
          if (!p) return;
          try {
            await api.revokeMCPApp(p.id);
          } catch (err) {
            toast.error(t('settings.apps.revokeFailed', { name: p.label }), { description: errorMessage(err) });
            throw err;
          }
          toast(t('settings.apps.revoked', { name: p.label }));
          refresh();
        }}
      />
    </SettingRow>
  );
}

function AppWizard({ secret: initial, projects, onClose }: { secret?: T.MCPPairingSecret; projects: T.Project[]; onClose: () => void }) {
  const t = useT();
  const [step, setStep] = useState<1 | 2 | 3>(initial ? 3 : 1);
  const [secret, setSecret] = useState<T.MCPPairingSecret | undefined>(initial);
  const [client, setClient] = useState<Client>(initial && isClient(initial.pairing.client) ? initial.pairing.client : 'claude');
  const [label, setLabel] = useState(() => t(`settings.apps.client.${client}` satisfies MessageKey));
  const [access, setAccess] = useState<Access>('chat');
  const [all, setAll] = useState(true);
  const [chosen, setChosen] = useState<string[]>([]);
  const projectList = useRef<HTMLUListElement>(null);
  // The list opens below the fold of a short window: bring all of it into view once it's there.
  useEffect(() => {
    if (!all) projectList.current?.scrollIntoView({ block: 'end' });
  }, [all]);
  const pair = useMutation({
    mutationFn: () =>
      api.pairMCPApp({
        label: label.trim(),
        client,
        access,
        ...(all ? {} : { projects: chosen }),
        ...(client === 'poke' || client === 'other' ? { token: true } : {}),
      }),
    onSuccess: (s) => {
      setSecret(s);
      setStep(3);
    },
  });
  const pickClient = (c: Client) => {
    // Keep a name the user typed; replace only the suggested one.
    if (clients.some((x) => label === t(`settings.apps.client.${x}` satisfies MessageKey))) setLabel(t(`settings.apps.client.${c}` satisfies MessageKey));
    setClient(c);
  };
  const noProjects = !all && chosen.length === 0;
  const steps = ['stepApp', 'stepAccess', 'stepConnect'] as const;

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-xl" data-apps-wizard>
        <DialogHeader>
          <DialogTitle>{t('settings.apps.wizard.title')}</DialogTitle>
          <p className="text-caption text-subtle">
            {step} / 3 · {t(`settings.apps.wizard.${steps[step - 1]}` satisfies MessageKey)}
          </p>
        </DialogHeader>

        {step === 1 && (
          <div className="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-label={t('settings.apps.wizard.stepApp')}>
            {clients.map((c) => (
              <button
                key={c}
                type="button"
                role="radio"
                aria-checked={client === c}
                data-client={c}
                onClick={() => pickClient(c)}
                className={cn(
                  'grid min-w-0 content-start gap-0.5 rounded-xl border px-4 py-3 text-left transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50',
                  client === c ? 'border-brand-500/60 bg-brand-500/10' : 'border-line bg-surface-faint hover:bg-surface-raised',
                )}
              >
                <span className="text-ui font-medium text-title">{t(`settings.apps.client.${c}` satisfies MessageKey)}</span>
                <span className="break-words text-caption text-muted">{t(`settings.apps.client.${c}.description` satisfies MessageKey)}</span>
              </button>
            ))}
          </div>
        )}

        {step === 2 && (
          <div className="grid min-w-0 gap-4">
            <Field label={t('settings.apps.wizard.nameLabel')} htmlFor="apps-name">
              <Input id="apps-name" value={label} placeholder={t('settings.apps.wizard.namePlaceholder')} onChange={(e) => setLabel(e.target.value)} />
            </Field>
            <div className="grid gap-2" role="radiogroup" aria-label={t('settings.apps.wizard.accessLabel')}>
              <p className="text-ui font-medium text-primary">{t('settings.apps.wizard.accessLabel')}</p>
              {accessLevels.map((a) => (
                <ChoiceRow key={a} checked={access === a} name="access" onSelect={() => setAccess(a)} title={t(`settings.apps.access.${a}` satisfies MessageKey)} description={t(`settings.apps.access.${a}.description` satisfies MessageKey)} />
              ))}
            </div>
            {access !== 'read' && <SettingNote tone="warning">{t('settings.apps.wizard.warning')}</SettingNote>}
            <div className="grid gap-2" role="radiogroup" aria-label={t('settings.apps.wizard.projectsLabel')}>
              <p className="text-ui font-medium text-primary">{t('settings.apps.wizard.projectsLabel')}</p>
              <ChoiceRow checked={all} name="projects" onSelect={() => setAll(true)} title={t('settings.apps.wizard.projectsAll')} />
              <ChoiceRow
                checked={!all}
                name="projects"
                onSelect={() => setAll(false)}
                title={t('settings.apps.wizard.projectsSome')}
              />
              {!all && (
                <ul ref={projectList} className="ml-6 grid max-h-44 scroll-mb-4 gap-1 overflow-y-auto rounded-lg border border-line bg-surface-faint p-2">
                  {projects.map((p) => (
                    <li key={p.name} className="min-w-0">
                      <label className="flex min-w-0 cursor-pointer items-center gap-2 text-ui text-secondary">
                        <input
                          type="checkbox"
                          className="shrink-0"
                          checked={chosen.includes(p.name)}
                          onChange={(e) => setChosen((c) => (e.target.checked ? [...c, p.name] : c.filter((n) => n !== p.name)))}
                        />
                        <span className="min-w-0 truncate">{p.displayName || p.name}</span>
                      </label>
                    </li>
                  ))}
                  {noProjects && <li className="text-caption text-subtle">{t('settings.apps.wizard.projectsNone')}</li>}
                </ul>
              )}
            </div>
            {pair.error && (
              <SettingNote tone="error">
                {t('settings.apps.wizard.failed')}: {errorMessage(pair.error)}
              </SettingNote>
            )}
          </div>
        )}

        {step === 3 && secret && (
          <div className="grid min-w-0 gap-4">
            <CopyField label={t('settings.apps.wizard.urlLabel')} value={secret.url} className="break-all font-mono text-caption" />
            <CopyField label={t('settings.apps.wizard.codeLabel')} value={secret.code} className="font-mono text-2xl font-semibold tracking-widest" hint={t('settings.apps.wizard.codeHint')} />
            {secret.token && (
              <CopyField label={t('settings.apps.wizard.tokenLabel')} value={secret.token} className="break-all font-mono text-caption" hint={t('settings.apps.wizard.tokenHint')} />
            )}
            <ol className="grid list-decimal gap-1.5 pl-5 text-ui leading-relaxed text-secondary">
              {([1, 2, 3] as const).map((n) => (
                <li key={n} className="min-w-0 break-words">
                  {t(`settings.apps.steps.${client}.${n}` satisfies MessageKey)}
                </li>
              ))}
            </ol>
          </div>
        )}

        <DialogFooter>
          {step === 2 && (
            <>
              <Button variant="ghost" disabled={pair.isPending} onClick={() => setStep(1)}>
                {t('settings.apps.wizard.back')}
              </Button>
              <Button variant="primary" disabled={pair.isPending || !label.trim() || noProjects} onClick={() => pair.mutate()}>
                {pair.isPending ? (
                  <>
                    <LoaderCircle className="size-3.5 animate-spin" /> {t('settings.apps.wizard.creating')}
                  </>
                ) : (
                  t('settings.apps.wizard.create')
                )}
              </Button>
            </>
          )}
          {step === 1 && (
            <Button variant="primary" onClick={() => setStep(2)}>
              {t('settings.apps.wizard.next')}
            </Button>
          )}
          {step === 3 && (
            <Button variant="primary" onClick={onClose}>
              {t('settings.apps.wizard.done')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ChoiceRow({ checked, name, onSelect, title, description }: { checked: boolean; name: string; onSelect: () => void; title: string; description?: string }) {
  return (
    <label
      className={cn(
        'flex min-w-0 cursor-pointer items-start gap-3 rounded-xl border px-3 py-2.5 transition',
        checked ? 'border-brand-500/60 bg-brand-500/10' : 'border-line bg-surface-faint hover:bg-surface-raised',
      )}
    >
      <input type="radio" name={name} className="mt-1 shrink-0" checked={checked} onChange={onSelect} />
      <span className="grid min-w-0 gap-0.5">
        <span className="text-ui font-medium text-title">{title}</span>
        {description && <span className="break-words text-caption text-muted">{description}</span>}
      </span>
    </label>
  );
}

function CopyField({ label, value, hint, className }: { label: string; value: string; hint?: string; className?: string }) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  return (
    <div className="grid min-w-0 gap-1.5">
      <p className="text-ui font-medium text-primary">{label}</p>
      <div className="flex min-w-0 items-start gap-2">
        <p className={cn('min-w-0 flex-1 rounded-lg border border-line bg-surface-faint px-3 py-2 text-primary [overflow-wrap:anywhere]', className)} data-copy-value>
          {value}
        </p>
        <Button
          size="sm"
          variant="secondary"
          className="shrink-0"
          onClick={() => {
            window.agentbox.copyText(value);
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          }}
        >
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />} {copied ? t('settings.apps.wizard.copied') : t('settings.apps.wizard.copy')}
        </Button>
      </div>
      {hint && <p className="text-xs text-subtle">{hint}</p>}
    </div>
  );
}
