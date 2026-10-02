import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ExternalLink, Globe, LoaderCircle, Lock, Plug, Plus, Unplug, X } from 'lucide-react';
import { type ReactNode, useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import * as A from '../../shared/api';
import { api } from '../lib/api';
import { projectLabel } from '../lib/projectName';
import {
  type ConnectorPreset,
  connectorName,
  connectorPresets,
  connectorStatus,
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

// Connectors, a section of a project's or an agent's Settings tab: remote MCP servers — Notion, Linear, Figma… — that a
// project's agents, or one agent, get as tools. A target is "pawly" or
// "pawly/agent-01", as under Secrets.
//
// The user signs in here, in their own browser, and the daemon keeps the
// tokens: no page can read one, so what a row shows is the connector's state
// and when its token runs out, never the token.

export function ConnectorsTab({ target }: { target: string }) {
  const queryClient = useQueryClient();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const forAgent = target.includes('/');
  const connectors = useQuery({ queryKey: ['connectors', target], queryFn: () => api.connectors(target) });
  const [removing, setRemoving] = useState<T.Connector | null>(null);
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['connectors'] });
  const connect = useConnect(target);

  const mine = connectors.data?.filter((c) => (forAgent ? c.scope === 'agent' : c.scope === 'project')) ?? [];
  const inherited = forAgent ? (connectors.data?.filter((c) => c.scope === 'project') ?? []) : [];
  const [project, agent] = target.split('/');

  return (
    <div className="h-full overflow-y-auto p-5">
      <div className="mx-auto grid max-w-4xl gap-4">
        <ConnectorRequestCards project={project} agent={agent} />

        <Catalog target={target} forAgent={forAgent} have={connectors.data ?? []} onAdded={refresh} connect={connect.start} />

        {inherited.length > 0 && (
          <Card
            title={`From the project (${inherited.length})`}
            icon={Lock}
            description="Every agent of this project gets these. Connect or change them in the project's Settings, under Connectors."
          >
            <ul className="grid gap-2" aria-label="Project connectors">
              {inherited.map((c) => (
                <ConnectorRow key={c.name} connector={c} />
              ))}
            </ul>
          </Card>
        )}

        <Card
          title={forAgent ? `This agent's own (${mine.length})` : `Project connectors (${mine.length})`}
          icon={Plug}
          description={
            forAgent
              ? "Only this agent gets these. One named like a project connector replaces the project's, for this agent alone."
              : 'Every agent of this project gets these as tools, including the ones you make later.'
          }
        >
          {connectors.error && <Notice>{errorMessage(connectors.error)}</Notice>}
          {connectors.isPending && <p className="py-3 text-sm text-subtle">Loading…</p>}
          {!connectors.isPending && mine.length === 0 && (
            <p className="py-3 text-[13px] leading-relaxed text-subtle">
              None yet. Pick one above. You sign in once, here; the agents reach it as <Code>mcp__notion__*</Code> tools and never hold the token.
            </p>
          )}
          {connect.error && <Notice>{connect.error}</Notice>}
          <ul className="grid gap-2" aria-label="Connectors">
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
        title={`Remove ${removing?.name}?`}
        description={`AgentBox forgets its sign-in and takes its tools away from ${
          removing?.scope === 'project' ? `every agent of ${projectLabel(removing?.project ?? '', projects.data)}` : removing?.agent
        }. A session already running keeps them until it next starts.`}
        confirmLabel="Remove"
        destructive
        onConfirm={async () => {
          await api.removeConnector(target, removing!.name);
          toast(`Removed ${removing!.name}`);
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
    if (settled.status === A.ConnectorConnected) toast.success(`${settled.name} connected`, { description: 'The agents have its tools from their next session.' });
    else if (settled.error) toast.error(`${settled.name} didn't connect`, { description: settled.error });
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
  forAgent,
  have,
  onAdded,
  connect,
}: {
  target: string;
  forAgent: boolean;
  have: T.Connector[];
  onAdded: () => Promise<unknown>;
  connect: (name: string) => Promise<void>;
}) {
  const [picked, setPicked] = useState<ConnectorPreset | 'custom' | null>(null);
  const added = (p: ConnectorPreset) => have.find((c) => c.url === p.url || c.name === p.name);

  return (
    <Card
      title={forAgent ? 'Give this agent a connector' : 'Give the project a connector'}
      icon={Plus}
      description={
        forAgent
          ? 'A remote MCP server this agent, and no other, gets as tools.'
          : 'A remote MCP server every agent of this project gets as tools, now and later.'
      }
    >
      <div className="grid gap-2 py-1 sm:grid-cols-5" role="list" aria-label="Catalog">
        {connectorPresets.map((p) => {
          const existing = added(p);
          return (
            <Tile
              key={p.id}
              label={p.label}
              detail={existing ? `added as ${existing.name}` : p.blurb}
              selected={picked !== 'custom' && picked?.id === p.id}
              disabled={!!existing}
              mark={<PresetMark preset={p} />}
              onClick={() => setPicked(picked !== 'custom' && picked?.id === p.id ? null : p)}
            />
          );
        })}
        <Tile
          label="Custom URL"
          detail="Any streamable HTTP server"
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
          taken={have.filter((c) => c.scope === (forAgent ? 'agent' : 'project')).map((c) => c.name)}
          onDone={async (connector) => {
            setPicked(null);
            await onAdded();
            if (connector.auth === A.ConnectorOAuth) await connect(connector.name);
            else toast(`${connector.name} added`, { description: connector.status === A.ConnectorConnected ? 'Its agents have its tools from their next session.' : undefined });
          }}
        />
      )}
    </Card>
  );
}

function Tile({
  label,
  detail,
  mark,
  selected,
  disabled,
  onClick,
}: {
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
      data-connector-preset={label}
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
// in a header, so it is kept, and shown, the way any other secret is.
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
        await api.setSecret(target, secretName.trim(), token);
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
          label="Name"
          htmlFor="connector-name"
          hint={taken.includes(shownName) ? 'There is one of that name already.' : 'What its tools are called: lowercase letters, digits, - and _.'}
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
        <Field label="Server URL" htmlFor="connector-url" hint="Its streamable HTTP endpoint, usually ending in /mcp.">
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
        <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label="How it signs in">
          {(
            [
              [A.ConnectorOAuth, 'Sign in with the browser'],
              [A.ConnectorSecret, 'A token in a header'],
              [A.ConnectorNone, 'Nothing: a public server'],
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
          <Field label={preset?.secret?.tokenLabel ?? 'Token'} htmlFor="connector-token" hint="Stored as a secret, encrypted. You can't read it back.">
            <Input
              id="connector-token"
              type="password"
              className="font-mono text-[13px]"
              placeholder="paste the token"
              autoComplete="off"
              spellCheck={false}
              value={token}
              onChange={(event) => setToken(event.target.value)}
            />
          </Field>
          <Field label="Kept as the secret" htmlFor="connector-secret">
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
          <Field label="Sent in the header" htmlFor="connector-header" hint={header.trim().toLowerCase() === 'authorization' ? 'As “Bearer <token>”.' : undefined}>
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
            How to make one
          </Button>
        ) : (
          <p className="text-xs leading-relaxed text-subtle">
            {auth === A.ConnectorOAuth ? 'Your browser opens to sign in. AgentBox keeps the token on this machine; the agents never see it.' : ''}
          </p>
        )}
        <Button type="submit" variant="primary" className="ml-auto" disabled={!ready || save.isPending}>
          {save.isPending ? <LoaderCircle className="animate-spin" /> : <Plug />}
          {auth === A.ConnectorOAuth ? 'Add and connect' : 'Add'}
        </Button>
      </div>
    </form>
  );
}

// ConnectorRow is one connector: its name and server, where it stands, how
// long its sign-in holds, and — when it is this target's own — what can be
// done about it. A project connector on an agent's tab shows only.
function ConnectorRow({
  connector: c,
  target,
  connecting,
  onConnect,
  onReopen,
  onRemove,
}: {
  connector: T.Connector;
  target?: string;
  connecting?: boolean;
  onConnect?: () => void;
  onReopen?: () => void;
  onRemove?: () => void;
}) {
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
        {!c.enabled && <Badge>off</Badge>}
        <span className="min-w-0 truncate font-mono text-[11.5px] text-subtle" title={c.url}>
          {c.url}
        </span>
        {target && (
          <div className="ml-auto flex shrink-0 items-center gap-1">
            <Tip label={c.enabled ? 'Given to agents' : 'Left out: it keeps its sign-in'}>
              <span className="flex items-center">
                <Switch
                  aria-label={`Give ${c.name} to agents`}
                  checked={c.enabled}
                  disabled={change.isPending}
                  onCheckedChange={(enabled) => change.mutate(() => api.setConnector(target, c.name, sameConnector(c, enabled)))}
                />
              </span>
            </Tip>
            {oauth && waiting && onReopen && (
              <Button size="sm" variant="ghost" onClick={onReopen}>
                <ExternalLink />
                Open the sign-in again
              </Button>
            )}
            {oauth && !waiting && c.status !== A.ConnectorConnected && (
              <Button size="sm" variant="primary" disabled={connecting} onClick={onConnect}>
                {connecting ? <LoaderCircle className="animate-spin" /> : <Plug />}
                {c.status === A.ConnectorError ? 'Connect again' : 'Connect'}
              </Button>
            )}
            {oauth && (c.status === A.ConnectorConnected || waiting) && (
              <Button size="sm" variant="ghost" disabled={change.isPending} onClick={() => change.mutate(() => api.disconnectConnector(target, c.name))}>
                <Unplug />
                {waiting ? 'Cancel' : 'Disconnect'}
              </Button>
            )}
            {onRemove && (
              <Tip label="Remove">
                <Button variant="ghost" size="icon-sm" aria-label={`Remove ${c.name}`} onClick={onRemove}>
                  <X />
                </Button>
              </Tip>
            )}
          </div>
        )}
      </div>
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 text-[11.5px] text-subtle">
        {token && <span data-connector-token>{token}</span>}
        {c.issuer && <span className="min-w-0 truncate">· signed in with {hostOf(c.issuer)}</span>}
        {c.connectedAt && c.status === A.ConnectorConnected && <span title={new Date(c.connectedAt).toLocaleString()}>· since {timeAgo(c.connectedAt, now)}</span>}
        {c.auth === A.ConnectorNone && <span>public server, no sign-in</span>}
        <span>{whereItIs(c)}</span>
      </div>
      {c.status === A.ConnectorError && c.error && <p className="break-words text-[11.5px] text-rose-300">{c.error}</p>}
      {change.error && <p className="break-words text-[11.5px] text-rose-300">{errorMessage(change.error)}</p>}
    </li>
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
  if (agents.length === 0) return '· in no agent yet';
  if (c.scope === 'agent') return '· in this agent';
  if (agents.length === 1) return `· in ${agents[0].split('/')[1]}`;
  return `· in ${agents.length} agents`;
}
