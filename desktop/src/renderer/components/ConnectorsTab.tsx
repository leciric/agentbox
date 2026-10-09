import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ExternalLink, Globe, LoaderCircle, Lock, Plug, Plus, Unplug, X } from 'lucide-react';
import { type ReactNode, useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import * as A from '../../shared/api';
import { api } from '../lib/api';
import { formatDateTime, t as translate, useT } from '../lib/i18n';
import { projectLabel } from '../lib/projectName';
import {
  type ConnectorOverride,
  type ConnectorPreset,
  connectorName,
  connectorPresets,
  connectorStatus,
  isWide,
  overrideOf,
  overrideRequest,
  tokenLine,
  validConnectorName,
  validConnectorURL,
} from '../lib/connectors';
import { useNow } from '../lib/useNow';
import { cn, errorMessage, timeAgo } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { ConnectorRequestCards } from './ConnectorRequestCard';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Card, Code, Notice } from './ui/card';
import { Field, Input } from './ui/input';
import { Switch } from './ui/switch';
import { Tip } from './ui/tooltip';

// Connectors, a section of a project's or an agent's Settings tab, and of
// AgentBox's Settings: remote MCP servers — Notion, Linear, Figma… — that every
// project, a project's agents, or one agent, get as tools. A target is "pawly"
// or "pawly/agent-01", as under Secrets, or "" for the AgentBox-wide ones.
// A project's page lists the AgentBox-wide ones it gets with a three-way
// override; one of its own of the same name replaces one there.
//
// The user signs in here, in their own browser, and the daemon keeps the
// tokens: no page can read one, so what a row shows is the connector's state
// and when its token runs out, never the token.

// embedded drops the tab's own scrolling and margins, inside a Settings page
// that has them.
export function ConnectorsTab({ target, embedded }: { target: string; embedded?: boolean }) {
  const t = useT();
  const queryClient = useQueryClient();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const wide = target === '';
  const forAgent = target.includes('/');
  const connectors = useQuery({ queryKey: ['connectors', target], queryFn: () => api.connectors(target) });
  const [removing, setRemoving] = useState<T.Connector | null>(null);
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['connectors'] });
  const connect = useConnect(target);

  const scope = wide ? A.ConnectorWide : forAgent ? 'agent' : 'project';
  const mine = connectors.data?.filter((c) => c.scope === scope) ?? [];
  const inherited = forAgent ? (connectors.data?.filter((c) => c.scope !== 'agent') ?? []) : [];
  const fromAgentBox = !wide && !forAgent ? (connectors.data?.filter(isWide) ?? []) : [];
  const [project, agent] = target.split('/');

  return (
    <div className={cn(!embedded && 'h-full overflow-y-auto p-5')}>
      <div className={cn('mx-auto grid gap-4', !embedded && 'max-w-4xl')}>
        {!wide && <ConnectorRequestCards project={project} agent={agent} />}

        <Catalog target={target} scope={scope} have={connectors.data ?? []} onAdded={refresh} connect={connect.start} />

        {fromAgentBox.length > 0 && (
          <Card
            title={t('project.connectors.fromAgentBox', { count: fromAgentBox.length })}
            icon={Globe}
            description={t('project.connectors.fromAgentBoxDescription')}
          >
            <ul className="grid gap-2" aria-label={t('project.connectors.agentBoxAria')}>
              {fromAgentBox.map((c) => (
                <ConnectorRow key={c.name} connector={c} control={<OverrideControl project={target} connector={c} />} />
              ))}
            </ul>
          </Card>
        )}

        {inherited.length > 0 && (
          <Card
            title={t('project.connectors.fromProject', { count: inherited.length })}
            icon={Lock}
            description={t('project.connectors.fromProjectDescription')}
          >
            <ul className="grid gap-2" aria-label={t('project.connectors.projectAria')}>
              {inherited.map((c) => (
                <ConnectorRow key={c.name} connector={c} />
              ))}
            </ul>
          </Card>
        )}

        <Card
          title={
            wide
              ? t('project.connectors.wideOwn', { count: mine.length })
              : forAgent
                ? t('project.connectors.agentOwn', { count: mine.length })
                : t('project.connectors.projectOwn', { count: mine.length })
          }
          icon={Plug}
          description={
            wide
              ? t('project.connectors.wideOwnDescription')
              : forAgent
                ? t('project.connectors.agentOwnDescription')
                : t('project.connectors.projectOwnDescription')
          }
        >
          {connectors.error && <Notice>{errorMessage(connectors.error)}</Notice>}
          {connectors.isPending && <p className="py-3 text-sm text-subtle">{t('common.loading')}</p>}
          {!connectors.isPending && mine.length === 0 && (
            <p className="py-3 text-[13px] leading-relaxed text-subtle">
              {t.rich('project.connectors.none', { code: (c) => <Code>{c}</Code> })}
            </p>
          )}
          {connect.error && <Notice>{connect.error}</Notice>}
          <ul className="grid gap-2" aria-label={t('project.connectors.listAria')}>
            {mine.map((c) => (
              <ConnectorRow
                key={c.name}
                connector={c}
                target={target}
                connecting={connect.starting === c.name}
                onConnect={() => connect.start(c.name)}
                onReopen={connect.reopen(c.name)}
                onRemove={() => setRemoving(c)}
              />
            ))}
          </ul>
        </Card>
      </div>

      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={t('project.connectors.removeTitle', { name: removing?.name ?? '' })}
        description={
          removing?.scope === A.ConnectorWide
            ? t('project.connectors.removeDescriptionWide')
            : removing?.scope === 'project'
              ? t('project.connectors.removeDescriptionProject', { project: projectLabel(removing.project ?? '', projects.data) })
              : t('project.connectors.removeDescriptionAgent', { agent: removing?.agent ?? '' })
        }
        confirmLabel={t('common.remove')}
        destructive
        onConfirm={async () => {
          await api.removeConnector(target, removing!.name);
          toast(t('project.connectors.removed', { name: removing!.name }));
          await refresh();
        }}
      />
    </div>
  );
}

// useConnect starts a sign-in: the daemon answers with the page to open, the
// user's own browser opens it, and the row turns connected when the daemon
// publishes the connector (lib/events.ts refetches the list). It says so
// when a sign-in it started finishes, one way or the other.
function useConnect(target: string) {
  const queryClient = useQueryClient();
  const [error, setError] = useState<string | undefined>();
  // starting is the connect call on its way; waiting, the sign-in after it,
  // until the connector stops waiting on the browser.
  const [starting, setStarting] = useState<string | undefined>();
  const [waiting, setWaiting] = useState<string | undefined>();
  const urls = useRef(new Map<string, string>());
  const connectors = useQuery({ queryKey: ['connectors', target], queryFn: () => api.connectors(target) });

  const start = async (name: string) => {
    setError(undefined);
    setStarting(name);
    try {
      const result = await api.connectConnector(target, name);
      urls.current.set(name, result.authorizationUrl);
      queryClient.setQueryData<T.Connector[]>(['connectors', target], (list) => list?.map((c) => (c.name === name ? result.connector : c)));
      setWaiting(name);
      await window.agentbox.openExternal(result.authorizationUrl);
    } catch (err) {
      setError(`${name}: ${errorMessage(err)}`);
    } finally {
      setStarting(undefined);
    }
  };

  const settled = waiting ? connectors.data?.find((c) => c.name === waiting && c.status !== A.ConnectorConnecting) : undefined;
  useEffect(() => {
    if (!settled) return;
    setWaiting(undefined);
    if (settled.status === A.ConnectorConnected) toast.success(translate('project.connectors.connected', { name: settled.name }), { description: translate('project.connectors.connectedDetail') });
    else if (settled.error) toast.error(translate('project.connectors.notConnected', { name: settled.name }), { description: settled.error });
  }, [settled]);

  const reopen = (name: string) => {
    const url = urls.current.get(name);
    return url ? () => void window.agentbox.openExternal(url) : undefined;
  };
  return { start, starting, error, reopen };
}

// Catalog is the servers the app knows, and a custom address. Picking one
// opens its form under the tiles: a name and URL for a browser sign-in, a
// token for a server that takes one instead.
function Catalog({
  target,
  scope,
  have,
  onAdded,
  connect,
}: {
  target: string;
  scope: string;
  have: T.Connector[];
  onAdded: () => Promise<unknown>;
  connect: (name: string) => Promise<void>;
}) {
  const t = useT();
  const [picked, setPicked] = useState<ConnectorPreset | 'custom' | null>(null);
  // An AgentBox-wide one doesn't take a project's tile: the project can add
  // its own, which replaces it there.
  const added = (p: ConnectorPreset) => have.find((c) => (scope === A.ConnectorWide || !isWide(c)) && (c.url === p.url || c.name === p.name));

  return (
    <Card
      title={
        scope === A.ConnectorWide
          ? t('project.connectors.giveWide')
          : scope === 'agent'
            ? t('project.connectors.giveAgent')
            : t('project.connectors.giveProject')
      }
      icon={Plus}
      description={
        scope === A.ConnectorWide
          ? t('project.connectors.giveWideDescription')
          : scope === 'agent'
            ? t('project.connectors.giveAgentDescription')
            : t('project.connectors.giveProjectDescription')
      }
    >
      <div className="grid gap-2 py-1 sm:grid-cols-5" role="list" aria-label={t('project.connectors.catalogAria')}>
        {connectorPresets.map((p) => {
          const existing = added(p);
          return (
            <Tile
              key={p.id}
              id={p.label}
              label={p.label}
              detail={existing ? t('project.connectors.addedAs', { name: existing.name }) : p.blurb}
              selected={picked !== 'custom' && picked?.id === p.id}
              disabled={!!existing}
              mark={<PresetMark preset={p} />}
              onClick={() => setPicked(picked !== 'custom' && picked?.id === p.id ? null : p)}
            />
          );
        })}
        <Tile
          id="Custom URL"
          label={t('project.connectors.customUrl')}
          detail={t('project.connectors.customDetail')}
          selected={picked === 'custom'}
          mark={<Globe className="size-4" />}
          onClick={() => setPicked(picked === 'custom' ? null : 'custom')}
        />
      </div>
      {picked && (
        <ConnectorForm
          key={picked === 'custom' ? 'custom' : picked.id}
          target={target}
          preset={picked === 'custom' ? undefined : picked}
          taken={have.filter((c) => c.scope === scope).map((c) => c.name)}
          onDone={async (connector) => {
            setPicked(null);
            await onAdded();
            if (connector.auth === A.ConnectorOAuth) await connect(connector.name);
            else toast(t('project.connectors.added', { name: connector.name }), { description: connector.status === A.ConnectorConnected ? t('project.connectors.addedDetail') : undefined });
          }}
        />
      )}
    </Card>
  );
}

function Tile({
  id,
  label,
  detail,
  mark,
  selected,
  disabled,
  onClick,
}: {
  id: string;
  label: string;
  detail: string;
  mark: ReactNode;
  selected: boolean;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="listitem"
      disabled={disabled}
      aria-pressed={selected}
      data-connector-preset={id}
      onClick={onClick}
      className={cn(
        'flex min-w-0 flex-col items-start gap-1.5 rounded-xl border px-3 py-2.5 text-left transition',
        selected ? 'border-brand-400/60 bg-brand-500/10' : 'border-line-faint bg-surface-faint hover:border-line-strong',
        'disabled:cursor-default disabled:opacity-55 disabled:hover:border-line-faint',
      )}
    >
      <span className="flex items-center gap-2 text-[13px] font-medium text-primary">
        <span className="flex size-6 items-center justify-center rounded-md bg-surface text-secondary ring-1 ring-inset ring-line">{mark}</span>
        {label}
      </span>
      <span className="line-clamp-2 w-full text-[11px] leading-snug text-subtle">{detail}</span>
    </button>
  );
}

// PresetMark is a letter, not the company's logo: the app ships no one else's
// artwork.
function PresetMark({ preset }: { preset: ConnectorPreset }) {
  return <span className="text-[11px] font-semibold">{preset.label[0]}</span>;
}

type AuthChoice = typeof A.ConnectorOAuth | typeof A.ConnectorSecret | typeof A.ConnectorNone;

// ConnectorForm adds a connector. For a browser sign-in that is a name and a
// URL, and the sign-in starts as soon as it is added; for a token, the token
// is stored as a secret of the same target first and the connector sends it
// in a header, so it is kept, and shown, the way any other secret is. An
// AgentBox-wide connector's token goes with it instead, kept by AgentBox and
// given to no agent.
function ConnectorForm({
  target,
  preset,
  taken,
  onDone,
}: {
  target: string;
  preset?: ConnectorPreset;
  taken: string[];
  onDone: (connector: T.Connector) => Promise<void>;
}) {
  const t = useT();
  const [url, setUrl] = useState(preset?.url ?? '');
  const [name, setName] = useState(preset?.name ?? '');
  const [nameTouched, setNameTouched] = useState(!!preset);
  const [auth, setAuth] = useState<AuthChoice>(preset?.auth ?? A.ConnectorOAuth);
  const [secretName, setSecretName] = useState(preset?.secret?.name ?? '');
  const [header, setHeader] = useState(preset?.secret?.header ?? 'Authorization');
  const [token, setToken] = useState('');
  const shownName = nameTouched ? name : connectorName(url);

  const save = useMutation({
    mutationFn: async () => {
      const req: T.SetConnectorRequest = { url: url.trim(), auth };
      if (auth === A.ConnectorSecret) {
        if (target === '') req.secretValue = token;
        else await api.setSecret(target, secretName.trim(), token);
        req.secret = secretName.trim();
        req.header = header.trim();
        // A preset says what goes before the token; a custom header leaves it
        // to the daemon, which puts Bearer before one in Authorization.
        if (preset?.secret) req.scheme = preset.secret.scheme;
      }
      return api.setConnector(target, shownName, req);
    },
    onSuccess: async (connector) => {
      setToken('');
      await onDone(connector);
    },
  });

  const nameOK = validConnectorName(shownName) && !taken.includes(shownName);
  const secretOK = auth !== A.ConnectorSecret || (/^[A-Z_][A-Z0-9_]*$/.test(secretName.trim()) && header.trim() !== '' && token !== '');
  const ready = nameOK && validConnectorURL(url.trim()) && secretOK;

  return (
    <form
      className="mt-3 grid gap-4 border-t border-line-faint pt-4"
      data-connector-form={preset?.id ?? 'custom'}
      onSubmit={(event) => {
        event.preventDefault();
        if (ready && !save.isPending) save.mutate();
      }}
    >
      {preset?.secret && <Notice tone="info">{preset.secret.why}</Notice>}
      <div className="grid gap-4 sm:grid-cols-[minmax(0,0.7fr)_minmax(0,1.6fr)]">
        <Field
          label={t('project.connectors.nameLabel')}
          htmlFor="connector-name"
          hint={taken.includes(shownName) ? t('project.connectors.nameTaken') : t('project.connectors.nameHint')}
        >
          <Input
            id="connector-name"
            className="font-mono text-[13px]"
            placeholder="acme"
            autoComplete="off"
            spellCheck={false}
            value={shownName}
            onChange={(event) => {
              setNameTouched(true);
              setName(event.target.value.toLowerCase());
            }}
          />
        </Field>
        <Field label={t('project.connectors.urlLabel')} htmlFor="connector-url" hint={t('project.connectors.urlHint')}>
          <Input
            id="connector-url"
            className="font-mono text-[13px]"
            placeholder="https://mcp.example.com/mcp"
            autoComplete="off"
            spellCheck={false}
            value={url}
            onChange={(event) => setUrl(event.target.value)}
          />
        </Field>
      </div>

      {!preset && (
        <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label={t('project.connectors.signInAria')}>
          {(
            [
              [A.ConnectorOAuth, t('project.connectors.authOAuth')],
              [A.ConnectorSecret, t('project.connectors.authSecret')],
              [A.ConnectorNone, t('project.connectors.authNone')],
            ] as const
          ).map(([value, label]) => (
            <Button key={value} type="button" size="sm" variant={auth === value ? 'primary' : 'ghost'} role="radio" aria-checked={auth === value} onClick={() => setAuth(value)}>
              {label}
            </Button>
          ))}
        </div>
      )}

      {auth === A.ConnectorSecret && (
        <div className="grid gap-4 sm:grid-cols-3">
          <Field
            label={preset?.secret?.tokenLabel ?? t('project.connectors.tokenLabel')}
            htmlFor="connector-token"
            hint={target === '' ? t('project.connectors.tokenHintWide') : t('project.connectors.tokenHint')}
          >
            <Input
              id="connector-token"
              type="password"
              className="font-mono text-[13px]"
              placeholder={t('project.connectors.tokenPlaceholder')}
              autoComplete="off"
              spellCheck={false}
              value={token}
              onChange={(event) => setToken(event.target.value)}
            />
          </Field>
          <Field label={t('project.connectors.secretLabel')} htmlFor="connector-secret">
            <Input
              id="connector-secret"
              className="font-mono text-[13px]"
              placeholder="ACME_TOKEN"
              autoComplete="off"
              spellCheck={false}
              value={secretName}
              onChange={(event) => setSecretName(event.target.value.toUpperCase())}
            />
          </Field>
          <Field label={t('project.connectors.headerLabel')} htmlFor="connector-header" hint={header.trim().toLowerCase() === 'authorization' ? t('project.connectors.headerHint') : undefined}>
            <Input
              id="connector-header"
              className="font-mono text-[13px]"
              autoComplete="off"
              spellCheck={false}
              value={header}
              onChange={(event) => setHeader(event.target.value)}
            />
          </Field>
        </div>
      )}

      {save.error && <Notice>{errorMessage(save.error)}</Notice>}
      <div className="flex items-center gap-3">
        {preset?.secret ? (
          <Button type="button" variant="ghost" size="sm" onClick={() => void window.agentbox.openExternal(preset.secret!.tokenUrl)}>
            <ExternalLink />
            {t('project.connectors.howToMake')}
          </Button>
        ) : (
          <p className="text-xs leading-relaxed text-subtle">
            {auth === A.ConnectorOAuth ? t('project.connectors.browserOpens') : ''}
          </p>
        )}
        <Button type="submit" variant="primary" className="ml-auto" disabled={!ready || save.isPending}>
          {save.isPending ? <LoaderCircle className="animate-spin" /> : <Plug />}
          {auth === A.ConnectorOAuth ? t('project.connectors.addAndConnect') : t('common.add')}
        </Button>
      </div>
    </form>
  );
}

// ConnectorRow is one connector: its name and server, where it stands, how
// long its sign-in holds, and — when it is this target's own — what can be
// done about it. A project connector on an agent's tab shows only; an
// AgentBox-wide one on a project's has control, its override.
function ConnectorRow({
  connector: c,
  target,
  control,
  connecting,
  onConnect,
  onReopen,
  onRemove,
}: {
  connector: T.Connector;
  target?: string;
  control?: ReactNode;
  connecting?: boolean;
  onConnect?: () => void;
  onReopen?: () => void;
  onRemove?: () => void;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const now = useNow(30_000);
  const status = connectorStatus(c);
  const token = tokenLine(c, now);
  const change = useMutation({
    mutationFn: (fn: () => Promise<T.Connector>) => fn(),
    onSuccess: (updated) => {
      queryClient.setQueryData<T.Connector[]>(['connectors', target], (list) => list?.map((x) => (x.name === updated.name && x.scope === updated.scope ? updated : x)));
    },
  });
  const oauth = c.auth === A.ConnectorOAuth;
  const waiting = c.status === A.ConnectorConnecting;

  return (
    <li
      data-connector={c.name}
      data-connector-status={c.status}
      className="grid gap-1.5 rounded-xl border border-line-faint bg-surface-faint px-3 py-2.5 transition hover:border-line-strong"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
        <span className="font-mono text-[13px] text-primary">{c.name}</span>
        <Badge variant={status.tone}>
          {waiting && <LoaderCircle className="animate-spin" />}
          {status.label}
        </Badge>
        {!c.enabled && <Badge>{t('project.connectors.off')}</Badge>}
        {isWide(c) && target === undefined && !control && <Badge variant="brand">{t('project.connectors.wideBadge')}</Badge>}
        <span className="min-w-0 truncate font-mono text-[11.5px] text-subtle" title={c.url}>
          {c.url}
        </span>
        {control && <div className="ml-auto flex shrink-0 items-center">{control}</div>}
        {target !== undefined && (
          <div className="ml-auto flex shrink-0 items-center gap-1">
            <Tip label={c.enabled ? t('project.connectors.given') : t('project.connectors.leftOut')}>
              <span className="flex items-center">
                <Switch
                  aria-label={t('project.connectors.giveSwitch', { name: c.name })}
                  checked={c.enabled}
                  disabled={change.isPending}
                  onCheckedChange={(enabled) => change.mutate(() => api.setConnector(target, c.name, sameConnector(c, enabled)))}
                />
              </span>
            </Tip>
            {oauth && waiting && onReopen && (
              <Button size="sm" variant="ghost" onClick={onReopen}>
                <ExternalLink />
                {t('project.connectors.reopen')}
              </Button>
            )}
            {oauth && !waiting && c.status !== A.ConnectorConnected && (
              <Button size="sm" variant="primary" disabled={connecting} onClick={onConnect}>
                {connecting ? <LoaderCircle className="animate-spin" /> : <Plug />}
                {c.status === A.ConnectorError ? t('project.connectors.connectAgain') : t('project.connectors.connect')}
              </Button>
            )}
            {oauth && (c.status === A.ConnectorConnected || waiting) && (
              <Button size="sm" variant="ghost" disabled={change.isPending} onClick={() => change.mutate(() => api.disconnectConnector(target, c.name))}>
                <Unplug />
                {waiting ? t('common.cancel') : t('project.connectors.disconnect')}
              </Button>
            )}
            {onRemove && (
              <Tip label={t('common.remove')}>
                <Button variant="ghost" size="icon-sm" aria-label={t('project.connectors.removeAria', { name: c.name })} onClick={onRemove}>
                  <X />
                </Button>
              </Tip>
            )}
          </div>
        )}
      </div>
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 text-[11.5px] text-subtle">
        {token && <span data-connector-token>{token}</span>}
        {c.issuer && <span className="min-w-0 truncate">{t('project.connectors.signedInWith', { host: hostOf(c.issuer) })}</span>}
        {c.connectedAt && c.status === A.ConnectorConnected && <span title={formatDateTime(c.connectedAt)}>{t('project.connectors.since', { when: timeAgo(c.connectedAt, now) })}</span>}
        {c.auth === A.ConnectorNone && <span>{t('project.connectors.publicServer')}</span>}
        <span>{whereItIs(c)}</span>
        {c.overrides && Object.keys(c.overrides).length > 0 && (
          <Tip label={Object.entries(c.overrides).map(([p, on]) => `${p}: ${on ? t('skills.on') : t('skills.off')}`).join(' · ')}>
            <span>{t('project.connectors.overridden', { count: Object.keys(c.overrides).length })}</span>
          </Tip>
        )}
      </div>
      {c.status === A.ConnectorError && c.error && <p className="break-words text-[11.5px] text-rose-300">{c.error}</p>}
      {change.error && <p className="break-words text-[11.5px] text-rose-300">{errorMessage(change.error)}</p>}
    </li>
  );
}

// OverrideControl is a project's say on an AgentBox-wide connector: off, on,
// or inherit, which follows the switch in AgentBox's Settings.
function OverrideControl({ project, connector: c }: { project: string; connector: T.Connector }) {
  const t = useT();
  const queryClient = useQueryClient();
  const set = useMutation({
    mutationFn: (o: ConnectorOverride) => api.setConnectorOverride(project, c.name, overrideRequest(o)),
    onSuccess: (updated) => {
      queryClient.setQueryData<T.Connector[]>(['connectors', project], (list) => list?.map((x) => (x.name === updated.name && x.scope === updated.scope ? updated : x)));
      void queryClient.invalidateQueries({ queryKey: ['connectors'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const current = set.isPending && set.variables ? set.variables : overrideOf(c);
  const options: [ConnectorOverride, string][] = [
    ['off', t('project.connectors.overrideOff')],
    ['inherit', t('project.connectors.overrideInherit')],
    ['on', t('project.connectors.overrideOn')],
  ];
  return (
    <div
      className="flex items-center rounded-lg border border-line-faint bg-surface p-0.5"
      role="radiogroup"
      aria-label={t('project.connectors.overrideAria', { name: c.name })}
      data-connector-override={current}
    >
      {options.map(([value, label]) => (
        <button
          key={value}
          type="button"
          role="radio"
          aria-checked={current === value}
          disabled={set.isPending}
          onClick={() => current !== value && set.mutate(value)}
          className={cn(
            'rounded-md px-2.5 py-1 text-[11.5px] font-medium transition',
            current === value ? 'bg-brand-500/15 text-primary ring-1 ring-inset ring-brand-400/50' : 'text-subtle hover:text-primary',
          )}
        >
          {label}
        </button>
      ))}
    </div>
  );
}

// sameConnector is the request that changes only whether it is given to
// agents: a PUT that left out how it signs in would default to OAuth, and
// changing that forgets the sign-in.
function sameConnector(c: T.Connector, enabled: boolean): T.SetConnectorRequest {
  return { url: c.url, auth: c.auth, secret: c.secret, header: c.header, scheme: c.scheme, enabled };
}

function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

// whereItIs says which agents get the connector, the way Secrets says
// which hold a secret.
function whereItIs(c: T.Connector): string {
  const agents = c.agents ?? [];
  if (agents.length === 0) return translate('project.connectors.inNone');
  if (isWide(c) && c.project === '' && agents.length > 1) {
    const projects = new Set(agents.map((ref) => ref.split('/')[0])).size;
    if (projects > 1) return translate('project.connectors.inManyProjects', { count: agents.length, projects });
  }
  if (c.scope === 'agent') return translate('project.connectors.inThisAgent');
  if (agents.length === 1) return translate('project.connectors.inOne', { agent: agents[0].split('/')[1] });
  return translate('project.connectors.inMany', { count: agents.length });
}
