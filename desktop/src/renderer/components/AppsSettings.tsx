import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Blocks, Check, Copy, KeyRound, LoaderCircle } from 'lucide-react';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { accessLevels, asAccess, asRoute, canPair, clients, clientsByRoute, forRoute, isClient, mcpSnippet, routes, routeURL, type Access, type Client, type DirectClient, type HubClient, type Route } from '../lib/mcpApps';
import { formatDateTime, t, useT, type MessageKey } from '../lib/i18n';
import type { SettingGroup } from '../lib/settingsSearch';
import { cn, errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input } from './ui/input';
import { SettingNote, SettingRow } from './ui/settings';
import { Switch } from './ui/switch';

// Using AgentBox from another app (Claude Code, Cursor, Claude, ChatGPT,
// Poke...) over remote MCP (internal/daemon, /v1/remote/mcp): where apps
// reach this computer, the apps paired with it, and the wizard that pairs a
// new one. An app reaches it one of three ways: on this computer, at a
// loopback address; through the Cloudflare tunnel phones use; or through the
// hub, the only one for apps that sign in with OAuth (claude.ai, ChatGPT).
// The first two need no hub, and the app sends its pairing's token.

export function appsGroups(): SettingGroup[] {
  return [
    {
      id: 'apps',
      title: t('settings.section.apps.title'),
      entries: [
        {
          id: 'apps-routes',
          label: t('settings.apps.routesLabel'),
          description: t('settings.apps.routesDescription'),
          keywords: t('settings.apps.routesKeywords'),
          render: () => <AppRoutes />,
        },
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

function AppRoutes() {
  const t = useT();
  const queryClient = useQueryClient();
  const apps = useQuery({ queryKey: ['mcp-apps'], queryFn: api.mcpApps });
  const st = apps.data;
  const tunnel = useMutation({
    mutationFn: (on: boolean) => api.updateMCPApps({ tunnel: on }),
    onSuccess: (next) => queryClient.setQueryData(['mcp-apps'], next),
    onError: (err) => toast.error(t('settings.apps.tunnel.changeFailed'), { description: errorMessage(err) }),
  });
  if (!st) return <SettingRow label={t('settings.apps.routesLabel')} description={t('settings.apps.routesDescription')} />;
  const tn = st.tunnel;
  const tunnelOn = tunnel.isPending ? !!tunnel.variables : tn.enabled;
  const localNote = st.local.error
    ? { tone: 'warning' as const, text: st.local.error }
    : { text: st.local.listening ? t('settings.apps.local.open') : t('settings.apps.local.closed') };
  const tunnelNote = !tn.enabled
    ? { text: t('settings.apps.tunnel.off') }
    : tn.state === 'running'
      ? {
          text:
            tn.mode === 'quick'
              ? t('settings.apps.tunnel.quick')
              : tn.transport === 'tailscale'
                ? t('settings.apps.tunnel.tailnet')
                : tn.mode === 'hub'
                  ? t('settings.apps.tunnel.hub')
                  : t('settings.apps.tunnel.named'),
          tone: tn.mode === 'quick' ? ('warning' as const) : undefined,
        }
      : tn.state === 'failed'
        ? { tone: 'error' as const, text: tn.error || t('settings.apps.tunnel.failed') }
        : { text: tn.error ? `${t('settings.apps.tunnel.starting')} ${tn.error}` : t('settings.apps.tunnel.starting') };
  const hubNote = st.available
    ? { text: t('settings.apps.hub.open') }
    : st.reason === 'no-hub'
      ? { text: t('settings.apps.noHub') }
      : st.reason === 'old-hub'
        ? { tone: 'warning' as const, text: t('settings.apps.oldHub', { hub: st.hub ?? '' }) }
        : st.reason === 'offline'
          ? { tone: 'warning' as const, text: t('settings.apps.offline', { hub: st.hub ?? '' }) }
          : { tone: 'error' as const, text: st.error || t('settings.apps.loadFailed') };
  const badge = (on: boolean, text: string) => (
    <Badge variant={on ? 'success' : 'default'} className="shrink-0">
      {text}
    </Badge>
  );

  return (
    <SettingRow label={t('settings.apps.routesLabel')} description={t('settings.apps.routesDescription')}>
      <ul className="grid gap-2" data-apps-routes>
        <RouteItem
          route="local"
          title={t('settings.apps.route.local')}
          url={st.local.url}
          note={localNote}
          status={badge(st.local.listening, st.local.listening ? t('settings.apps.state.open') : t('settings.apps.state.idle'))}
        />
        <RouteItem
          route="tunnel"
          title={t('settings.apps.route.tunnel')}
          url={routeURL(st, 'tunnel')}
          note={tunnelNote}
          status={
            tn.enabled && tn.state !== 'off'
              ? badge(tn.state === 'running', t(`settings.apps.state.${tn.state === 'running' ? 'running' : tn.state === 'failed' ? 'failed' : 'starting'}` satisfies MessageKey))
              : undefined
          }
          control={
            <Switch
              checked={tunnelOn}
              disabled={tunnel.isPending}
              aria-label={t('settings.apps.tunnel.toggle')}
              data-apps-tunnel
              onCheckedChange={(on) => tunnel.mutate(on)}
            />
          }
        />
        <RouteItem
          route="hub"
          title={t('settings.apps.route.hub')}
          url={routeURL(st, 'hub')}
          note={hubNote}
          status={badge(
            st.available,
            t(`settings.apps.state.${st.available ? 'connected' : st.reason === 'offline' ? 'offline' : st.reason === 'old-hub' ? 'oldHub' : 'none'}` satisfies MessageKey),
          )}
        />
      </ul>
    </SettingRow>
  );
}

function RouteItem({
  route,
  title,
  url,
  note,
  status,
  control,
}: {
  route: Route;
  title: string;
  url?: string;
  note: { text: string; tone?: 'warning' | 'error' };
  status?: ReactNode;
  control?: ReactNode;
}) {
  return (
    <li className="grid min-w-0 gap-2 rounded-xl border border-line px-3 py-2.5" data-route={route}>
      <div className="flex min-w-0 items-center gap-2">
        <p className="min-w-0 flex-1 truncate text-ui font-medium text-primary">{title}</p>
        {status}
        {control}
      </div>
      {url && <CopyField value={url} className="break-all font-mono text-caption" />}
      <SettingNote tone={note.tone} className="[overflow-wrap:anywhere]">
        {note.text}
      </SettingNote>
    </li>
  );
}

function PairedApps() {
  const t = useT();
  const queryClient = useQueryClient();
  const apps = useQuery({ queryKey: ['mcp-apps'], queryFn: api.mcpApps });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  // null: closed; otherwise the wizard, opened on its last step when a new code or token was made.
  const [wizard, setWizard] = useState<{ key: number; secret?: T.MCPPairingSecret } | null>(null);
  const [revoking, setRevoking] = useState<T.MCPPairing | null>(null);
  const st = apps.data;
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
        <Button size="sm" variant="primary" data-apps-pair disabled={!st} onClick={() => setWizard({ key: Date.now() })}>
          <Blocks className="size-3.5" /> {t('settings.apps.pair')}
        </Button>
      }
    >
      <div className="grid gap-3">
        {apps.error && <SettingNote tone="error">{t('settings.apps.loadFailed')}: {errorMessage(apps.error)}</SettingNote>}
        {st && st.pairings.length === 0 && <SettingNote>{t('settings.apps.empty')}</SettingNote>}
        {st && st.pairings.length > 0 && (
          <ul className="grid gap-2" data-apps>
            {st.pairings.map((p) => {
              const route = asRoute(p.route);
              return (
                <li key={p.id} className="grid min-w-0 gap-2 rounded-xl border border-line px-3 py-2.5" data-app={p.id} data-app-route={route}>
                  <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
                    <div className="min-w-0 flex-1 basis-48">
                      <div className="flex min-w-0 flex-wrap items-center gap-2">
                        <p className="min-w-0 truncate text-ui font-medium text-primary">{p.label}</p>
                        <Badge variant={p.access === 'read' ? 'default' : p.access === 'chat' ? 'brand' : 'warning'} className="shrink-0">
                          {t(`settings.apps.access.${asAccess(p.access)}` satisfies MessageKey)}
                        </Badge>
                        <Badge variant="info" className="shrink-0">
                          {t(`settings.apps.routeBadge.${route}` satisfies MessageKey)}
                        </Badge>
                      </div>
                      <p className="break-words text-caption text-subtle [overflow-wrap:anywhere]">
                        {p.projects.length === 0
                          ? t('settings.apps.allProjects')
                          : p.projects.length <= 3
                            ? p.projects.map((n) => names(projects.data, n)).join(', ')
                            : t('settings.apps.projectCount', { n: p.projects.length })}
                        {route === 'hub' && (
                          <>
                            {' · '}
                            {p.apps && p.apps.length > 0 ? t('settings.apps.signedIn', { apps: p.apps.join(', ') }) : t('settings.apps.notSignedIn')}
                          </>
                        )}
                        {' · '}
                        {when(p.lastUsedAt)}
                      </p>
                    </div>
                    <div className="flex shrink-0 gap-2">
                      <Button
                        size="sm"
                        variant="secondary"
                        title={route === 'hub' ? t('settings.apps.newCodeHint') : t('settings.apps.newTokenHint')}
                        disabled={(route === 'hub' && !st.available) || newCode.isPending}
                        onClick={() => newCode.mutate(p.id)}
                      >
                        <KeyRound className="size-3.5" /> {route === 'hub' ? t('settings.apps.newCode') : t('settings.apps.newToken')}
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => setRevoking(p)}>
                        {t('settings.apps.revoke')}
                      </Button>
                    </div>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </div>
      {wizard && st && (
        <AppWizard
          key={wizard.key}
          secret={wizard.secret}
          status={st}
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

function AppWizard({
  secret: initial,
  status,
  projects,
  onClose,
}: {
  secret?: T.MCPPairingSecret;
  status: T.MCPPairings;
  projects: T.Project[];
  onClose: () => void;
}) {
  const t = useT();
  const [step, setStep] = useState<1 | 2 | 3>(initial ? 3 : 1);
  const [secret, setSecret] = useState<T.MCPPairingSecret | undefined>(initial);
  const [route, setRoute] = useState<Route>(initial ? asRoute(initial.pairing.route) : 'local');
  const [client, setClient] = useState<Client>(initial && isClient(initial.pairing.client) ? initial.pairing.client : clientsByRoute[route][0]);
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
        route,
        client,
        access,
        ...(all ? {} : { projects: chosen }),
        ...(route === 'hub' && (client === 'poke' || client === 'other') ? { token: true } : {}),
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
  const pickRoute = (r: Route) => {
    setRoute(r);
    if (!forRoute(r, client)) pickClient(clientsByRoute[r][0]);
  };
  const noProjects = !all && chosen.length === 0;
  const steps = ['stepApp', 'stepAccess', 'stepConnect'] as const;
  const shownRoute = secret ? asRoute(secret.pairing.route) : route;
  const routeHint = (r: Route): string =>
    r === 'tunnel' && !status.tunnel.enabled
      ? t('settings.apps.wizard.route.tunnelOff')
      : r === 'hub' && !status.available
        ? t('settings.apps.wizard.route.hubOff')
        : t(`settings.apps.wizard.route.${r}.description` satisfies MessageKey);

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
          <div className="grid min-w-0 gap-4">
            <div className="grid gap-2" role="radiogroup" aria-label={t('settings.apps.wizard.routeLabel')}>
              <p className="text-ui font-medium text-primary">{t('settings.apps.wizard.routeLabel')}</p>
              {routes.map((r) => (
                <ChoiceRow
                  key={r}
                  checked={route === r}
                  disabled={!canPair(status, r)}
                  name="route"
                  data-route-choice={r}
                  onSelect={() => pickRoute(r)}
                  title={t(`settings.apps.route.${r}` satisfies MessageKey)}
                  description={routeHint(r)}
                />
              ))}
            </div>
            <div className="grid gap-2" role="radiogroup" aria-label={t('settings.apps.wizard.stepApp')}>
              <p className="text-ui font-medium text-primary">{t('settings.apps.wizard.stepApp')}</p>
              <div className="grid gap-2 sm:grid-cols-2">
                {clientsByRoute[route].map((c) => (
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
            </div>
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

        {step === 3 && secret && (shownRoute === 'hub' ? <HubConnect secret={secret} client={client} /> : <DirectConnect secret={secret} route={shownRoute} client={client} status={status} />)}

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
            <Button variant="primary" disabled={!canPair(status, route) || !forRoute(route, client)} onClick={() => setStep(2)}>
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

// HubConnect is the last step for an app paired through the hub: its URL,
// the code it signs in with, and a token for one that can't.
function HubConnect({ secret, client }: { secret: T.MCPPairingSecret; client: Client }) {
  const t = useT();
  const c: HubClient = forRoute('hub', client) ? (client as HubClient) : 'other';
  return (
    <div className="grid min-w-0 gap-4">
      <CopyField label={t('settings.apps.wizard.urlLabel')} value={secret.url} className="break-all font-mono text-caption" />
      {secret.code && <CopyField label={t('settings.apps.wizard.codeLabel')} value={secret.code} className="font-mono text-2xl font-semibold" hint={t('settings.apps.wizard.codeHint')} />}
      {secret.token && <CopyField label={t('settings.apps.wizard.tokenLabel')} value={secret.token} className="break-all font-mono text-caption" hint={t('settings.apps.wizard.tokenHint')} />}
      <ol className="grid list-decimal gap-1.5 pl-5 text-ui leading-relaxed text-secondary">
        {([1, 2, 3] as const).map((n) => (
          <li key={n} className="min-w-0 break-words">
            {t(`settings.apps.steps.${c}.${n}` satisfies MessageKey)}
          </li>
        ))}
      </ol>
    </div>
  );
}

// DirectConnect is the last step for an app paired on this computer or
// through the tunnel: its URL and token, and what to paste into it.
function DirectConnect({ secret, route, client, status }: { secret: T.MCPPairingSecret; route: Route; client: Client; status: T.MCPPairings }) {
  const t = useT();
  const c: DirectClient = client === 'claude' || client === 'chatgpt' ? 'other' : client;
  const token = secret.token ?? '';
  // A tunnel paired while it starts has no address yet: it comes with the
  // status, once it runs.
  const url = secret.url || routeURL(status, route) || '';
  const snippet = url ? mcpSnippet(c, url, token) : undefined;
  return (
    <div className="grid min-w-0 gap-4" data-apps-connect={route}>
      {url ? (
        <CopyField label={t('settings.apps.wizard.urlLabel')} value={url} className="break-all font-mono text-caption" />
      ) : (
        <SettingNote tone="muted">{t('settings.apps.tunnel.starting')}</SettingNote>
      )}
      <CopyField label={t('settings.apps.wizard.tokenLabel')} value={token} className="break-all font-mono text-caption" hint={t('settings.apps.wizard.tokenHintDirect')} />
      {snippet && (
        <CopyField
          label={t(`settings.apps.snippet.${c === 'claude-code' ? 'command' : 'config'}` satisfies MessageKey)}
          value={snippet}
          pre
          className="font-mono text-caption"
        />
      )}
      <ol className="grid list-decimal gap-1.5 pl-5 text-ui leading-relaxed text-secondary">
        {([1, 2] as const).map((n) => (
          <li key={n} className="min-w-0 break-words">
            {t(`settings.apps.direct.${c}.${n}` satisfies MessageKey)}
          </li>
        ))}
      </ol>
      {route === 'tunnel' && status.tunnel.mode === 'quick' && <SettingNote tone="warning">{t('settings.apps.tunnel.quick')}</SettingNote>}
    </div>
  );
}

function ChoiceRow({
  checked,
  disabled,
  name,
  onSelect,
  title,
  description,
  ...props
}: {
  checked: boolean;
  disabled?: boolean;
  name: string;
  onSelect: () => void;
  title: string;
  description?: string;
  'data-route-choice'?: string;
}) {
  return (
    <label
      className={cn(
        'flex min-w-0 items-start gap-3 rounded-xl border px-3 py-2.5 transition',
        disabled ? 'cursor-not-allowed border-line opacity-60' : 'cursor-pointer',
        checked && !disabled ? 'border-brand-500/60 bg-brand-500/10' : !disabled && 'border-line bg-surface-faint hover:bg-surface-raised',
      )}
      {...props}
    >
      <input type="radio" name={name} className="mt-1 shrink-0" checked={checked && !disabled} disabled={disabled} onChange={onSelect} />
      <span className="grid min-w-0 gap-0.5">
        <span className="text-ui font-medium text-title">{title}</span>
        {description && <span className="break-words text-caption text-muted">{description}</span>}
      </span>
    </label>
  );
}

function CopyField({ label, value, hint, className, pre }: { label?: string; value: string; hint?: string; className?: string; pre?: boolean }) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  const box = 'min-w-0 flex-1 rounded-lg border border-line bg-surface-faint px-3 py-2 text-primary';
  return (
    <div className="grid min-w-0 gap-1.5">
      {label && <p className="text-ui font-medium text-primary">{label}</p>}
      <div className="flex min-w-0 items-start gap-2">
        {pre ? (
          <pre className={cn(box, 'max-h-56 overflow-auto whitespace-pre', className)} data-copy-value>
            {value}
          </pre>
        ) : (
          <p className={cn(box, '[overflow-wrap:anywhere]', className)} data-copy-value>
            {value}
          </p>
        )}
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
