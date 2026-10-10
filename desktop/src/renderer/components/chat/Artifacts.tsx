import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Clock, ExternalLink, Globe, Infinity as Forever, Lock, MessageSquareText, RefreshCw, TriangleAlert } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import * as T from '../../../shared/api';
import { api, isHomeChat } from '../../lib/api';
import { artifactsFor, artifactsKey, byState, expiryOf, type Expiry } from '../../lib/artifacts';
import { useT } from '../../lib/i18n';
import { useNow } from '../../lib/useNow';
import { cn, errorMessage, timeAgo, timeUntil } from '../../lib/utils';
import { AIIcon } from '../state';
import { Badge } from '../ui/badge';
import { Button } from '../ui/button';
import { Dialog, DialogContent, DialogTitle } from '../ui/dialog';
import { Tip } from '../ui/tooltip';

// The pages a project's chats published on Hatch, over the chat's composer:
// in the project's chat every one, with the agent that made it; in an
// agent's, its own. Nothing shows until the project has Hatch connected and
// one of its chats has published a page (lib/artifacts.ts).

// HatchMark is Hatch's egg with a crack through it, as Settings → Connectors
// draws it.
export function HatchMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={cn('size-4', className)} fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M8 1.8c2.6 0 4.6 3.4 4.6 6.4a4.6 4.6 0 0 1-9.2 0C3.4 5.2 5.4 1.8 8 1.8Z" />
      <path d="m4 8.4 1.8 1.4L8 7.8l2.2 2L12 8.4" />
    </svg>
  );
}

// ArtifactTray is the strip of a chat's artifacts. onReveal brings the call
// that made one into view, for those made in this chat.
export function ArtifactTray({ agent, onReveal }: { agent: T.Agent; onReveal?: (item: string) => void }) {
  const t = useT();
  const project = agent.project;
  const home = isHomeChat(agent.ref);
  const query = useQuery({ queryKey: artifactsKey(project), queryFn: () => api.artifacts(project), enabled: !home, staleTime: 60_000 });
  const now = useNow(60_000);
  const [open, setOpen] = useState<string>();
  const lead = agent.name === T.LeadName;
  const list = byState(artifactsFor(query.data, agent.ref), now);
  if (home || list.length === 0) return null;
  const opened = list.find((a) => a.id === open);

  return (
    <>
      {/* Over the end of the conversation: only the label and the chips take clicks. */}
      <div className="pointer-events-none mb-2 flex animate-fade-in items-center gap-2 px-1 [&>*]:pointer-events-auto" data-chat-artifacts>
        <Tip label={t('chat.artifacts.tip')}>
          <span className="flex shrink-0 items-center gap-1.5 rounded-full border border-line bg-composer/80 py-1 pl-2 pr-2.5 text-[11px] font-medium text-muted backdrop-blur-xl">
            <HatchMark className="size-3.5 text-amber-300" />
            {t('chat.artifacts.label')}
            <span className="tabular-nums text-subtle">{list.length}</span>
          </span>
        </Tip>
        <div className="flex min-w-0 flex-1 gap-1.5 overflow-x-auto py-0.5 [mask-image:linear-gradient(to_right,black_calc(100%-24px),transparent)] [scrollbar-width:none]">
          {list.map((a) => (
            <ArtifactChip key={a.id} artifact={a} lead={lead} now={now} onOpen={() => setOpen(a.id)} />
          ))}
        </div>
      </div>
      <Dialog open={!!opened} onOpenChange={(next) => !next && setOpen(undefined)}>
        {opened && (
          <ArtifactPreview
            artifact={opened}
            chatRef={agent.ref}
            onReveal={
              onReveal && opened.agent === agent.ref
                ? () => {
                    setOpen(undefined);
                    onReveal(opened.item);
                  }
                : undefined
            }
          />
        )}
      </Dialog>
    </>
  );
}

function ArtifactChip({ artifact: a, lead, now, onOpen }: { artifact: T.Artifact; lead: boolean; now: number; onOpen: () => void }) {
  const t = useT();
  const expiry = expiryOf(a, now);
  const gone = expiry === 'expired';
  return (
    <button
      onClick={onOpen}
      data-artifact={a.id}
      className={cn(
        'group flex w-[220px] shrink-0 items-center gap-2.5 rounded-xl border border-line-strong bg-composer/85 px-2 py-1.5 text-left shadow-[0_12px_30px_-20px_var(--ab-shadow-deep)] backdrop-blur-xl transition hover:border-line-vivid hover:bg-surface-strong',
        gone && 'opacity-60 hover:opacity-100',
      )}
    >
      <PageTile gone={gone} />
      <span className="grid min-w-0 flex-1 gap-0.5">
        <span className={cn('truncate text-[12.5px] font-medium leading-tight text-title', gone && 'text-muted line-through decoration-faint')}>{a.title}</span>
        <span className="flex min-w-0 items-center gap-1 text-[10.5px] leading-tight text-subtle">
          {lead && (
            <>
              <span className="min-w-0 truncate">
                <AgentName agentRef={a.agent} />
              </span>
              <span aria-hidden>·</span>
            </>
          )}
          <ExpiryText artifact={a} expiry={expiry} now={now} short />
          {!lead && <span className="truncate">{t('chat.artifacts.version', { n: a.version })}</span>}
        </span>
      </span>
    </button>
  );
}

// PageTile is a page's thumbnail: a sheet with lines on it, warm while the
// page is there and grey once it's gone.
function PageTile({ gone, large }: { gone: boolean; large?: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        'relative flex shrink-0 flex-col justify-center gap-[3px] overflow-hidden rounded-lg border px-1.5',
        large ? 'size-10 gap-1 px-2' : 'size-8',
        gone ? 'border-line bg-surface' : 'border-amber-300/25 bg-gradient-to-br from-amber-300/20 via-orange-400/10 to-rose-400/10',
      )}
    >
      <span className={cn('h-[3px] w-3/5 rounded-full', gone ? 'bg-faint' : 'bg-amber-200/70')} />
      <span className={cn('h-[2px] w-full rounded-full', gone ? 'bg-ghost' : 'bg-amber-100/30')} />
      <span className={cn('h-[2px] w-4/5 rounded-full', gone ? 'bg-ghost' : 'bg-amber-100/30')} />
    </span>
  );
}

// AgentName is who made an artifact: an agent by its title, the project's
// chat as such.
function AgentName({ agentRef }: { agentRef: string }) {
  const t = useT();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents, staleTime: 30_000 });
  const name = agentRef.split('/')[1] ?? agentRef;
  if (name === T.LeadName) return <>{t('chat.project.agentTitle')}</>;
  const found = agents.data?.find((a) => a.ref === agentRef);
  return <>{found?.title || name}</>;
}

function agentAI(agents: T.Agent[] | undefined, ref: string): string {
  return agents?.find((a) => a.ref === ref)?.ai ?? 'claude';
}

function ExpiryText({ artifact: a, expiry, now, short }: { artifact: T.Artifact; expiry: Expiry; now: number; short?: boolean }) {
  const t = useT();
  switch (expiry) {
    case 'expired':
      return <span className="shrink-0 font-medium text-rose-300">{t('chat.artifacts.expired')}</span>;
    case 'permanent':
      return <span className="shrink-0">{short ? timeAgo(a.updatedAt, now) : t('chat.artifacts.permanent')}</span>;
    case 'unknown':
      return <span className="shrink-0">{timeAgo(a.updatedAt, now)}</span>;
  }
  const left = timeUntil(a.expiresAt!, now);
  return (
    <span className={cn('shrink-0', expiry === 'soon' && 'font-medium text-amber-300')}>
      {t(short ? 'chat.artifacts.expiresShort' : 'chat.artifacts.expires', { when: left })}
    </span>
  );
}

// ArtifactPreview is one artifact, large: what it is, who made it and when,
// how long it has left, and the page itself in a sandboxed frame. The page's
// HTML comes through the daemon, with the Hatch connector's sign-in, since a
// private page's link only opens for the user signed in to Hatch in a browser.
function ArtifactPreview({ artifact, chatRef, onReveal }: { artifact: T.Artifact; chatRef: string; onReveal?: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const project = chatRef.split('/')[0];
  const now = useNow(30_000);
  const [reloads, setReloads] = useState(0);
  const [loaded, setLoaded] = useState(false);
  const preview = useQuery({
    queryKey: ['artifactPreview', project, artifact.id, reloads],
    queryFn: () => api.artifactPreview(project, artifact.id),
    retry: false,
    staleTime: 0,
    gcTime: 0,
  });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents, staleTime: 30_000 });
  // What Hatch says, once it has: the title, the version and the expiry.
  const a = preview.data?.artifact ?? artifact;
  const expiry = expiryOf(a, now);
  const status = preview.data?.status;
  const gone = status === T.ArtifactExpired || expiry === 'expired';
  const reload = () => {
    setLoaded(false);
    setReloads((n) => n + 1);
    void queryClient.invalidateQueries({ queryKey: artifactsKey(project) });
  };
  const browser = () => void window.agentbox.openExternal(a.url);

  let body: ReactNode;
  if (preview.isPending) {
    body = <div className="skeleton absolute inset-0 rounded-none" />;
  } else if (preview.error) {
    body = <Empty icon={<TriangleAlert className="size-5" />} title={t('chat.artifacts.unavailable')} text={errorMessage(preview.error)} onBrowser={browser} />;
  } else if (gone) {
    body = <Empty icon={<HatchMark className="size-5" />} title={t('chat.artifacts.goneTitle')} text={t('chat.artifacts.goneText')} />;
  } else if (status === T.ArtifactUnavailable) {
    body = <Empty icon={<TriangleAlert className="size-5" />} title={t('chat.artifacts.unavailable')} text={preview.data.error ?? ''} onBrowser={browser} />;
  } else if (!preview.data.page) {
    body = <Empty icon={<Globe className="size-5" />} title={t('chat.artifacts.noPreview')} text={t('chat.artifacts.noPreviewText')} onBrowser={browser} />;
  } else {
    body = (
      <>
        {!loaded && <div className="skeleton absolute inset-0 rounded-none" />}
        <iframe
          key={`${a.version}#${reloads}`}
          title={a.title}
          src={api.artifactUrl(project, a.id)}
          // No allow-same-origin: the page gets an opaque origin, and can't
          // reach the app, its storage or the daemon. The daemon's answer
          // carries Hatch's own sandboxing policy too.
          sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox allow-modals"
          referrerPolicy="no-referrer"
          onLoad={() => setLoaded(true)}
          className={cn('absolute inset-0 size-full border-0 bg-white transition-opacity duration-300', loaded ? 'opacity-100' : 'opacity-0')}
        />
      </>
    );
  }

  return (
    <DialogContent
      className="flex h-[86vh] w-[calc(100vw-3rem)] max-w-[1180px] flex-col gap-0 overflow-hidden p-0"
      data-artifact-preview={a.id}
      // Focusing the first button would open its tooltip over the page.
      onOpenAutoFocus={(event) => event.preventDefault()}
    >
      <div className="flex items-start gap-3 border-b border-line px-5 py-4 pr-14">
        <PageTile gone={gone} large />
        <div className="grid min-w-0 flex-1 gap-1">
          <DialogTitle className="truncate text-[15px] leading-snug">{a.title}</DialogTitle>
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-muted">
            <span className="flex min-w-0 items-center gap-1.5">
              <AIIcon ai={agentAI(agents.data, a.agent)} className="size-3.5 shrink-0" />
              <span className="truncate text-secondary">
                <AgentName agentRef={a.agent} />
              </span>
            </span>
            <span aria-hidden className="text-faint">·</span>
            <Tip label={new Date(a.createdAt).toLocaleString()}>
              <span>{t('chat.artifacts.published', { when: timeAgo(a.createdAt, now) })}</span>
            </Tip>
            {a.version > 1 && (
              <>
                <span aria-hidden className="text-faint">·</span>
                <Tip label={new Date(a.updatedAt).toLocaleString()}>
                  <span>{t('chat.artifacts.updated', { n: a.version, when: timeAgo(a.updatedAt, now) })}</span>
                </Tip>
              </>
            )}
            {a.agents.length > 1 && (
              <>
                <span aria-hidden className="text-faint">·</span>
                <span>{t('chat.artifacts.alsoBy', { count: a.agents.length - 1 })}</span>
              </>
            )}
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            <ExpiryBadge artifact={a} expiry={gone ? 'expired' : expiry} now={now} />
            {a.public ? (
              <Badge>
                <Globe />
                {t('chat.artifacts.public')}
              </Badge>
            ) : (
              <Badge>
                <Lock />
                {t('chat.artifacts.private')}
              </Badge>
            )}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {onReveal && (
            <Tip label={t('chat.artifacts.revealTip')}>
              <Button size="icon-sm" variant="ghost" onClick={onReveal} aria-label={t('chat.artifacts.revealTip')}>
                <MessageSquareText />
              </Button>
            </Tip>
          )}
          <Tip label={t('chat.artifacts.reload')}>
            <Button size="icon-sm" variant="ghost" onClick={reload} disabled={preview.isFetching} aria-label={t('chat.artifacts.reload')}>
              <RefreshCw className={cn(preview.isFetching && 'animate-spin')} />
            </Button>
          </Tip>
          <Button size="sm" variant="secondary" onClick={browser}>
            <ExternalLink />
            {t('chat.artifacts.openInBrowser')}
          </Button>
        </div>
      </div>
      <div className="relative min-h-0 flex-1 bg-sunken">{body}</div>
    </DialogContent>
  );
}

function ExpiryBadge({ artifact: a, expiry, now }: { artifact: T.Artifact; expiry: Expiry; now: number }) {
  const t = useT();
  switch (expiry) {
    case 'expired':
      return (
        <Badge variant="danger">
          <Clock />
          {t('chat.artifacts.expired')}
        </Badge>
      );
    case 'permanent':
      return (
        <Badge variant="success">
          <Forever />
          {t('chat.artifacts.permanent')}
        </Badge>
      );
    case 'unknown':
      return null;
  }
  return (
    <Tip label={new Date(a.expiresAt!).toLocaleString()}>
      <Badge variant={expiry === 'soon' ? 'warning' : 'default'}>
        <Clock />
        {t('chat.artifacts.expires', { when: timeUntil(a.expiresAt!, now) })}
      </Badge>
    </Tip>
  );
}

function Empty({ icon, title, text, onBrowser }: { icon: ReactNode; title: string; text: string; onBrowser?: () => void }) {
  const t = useT();
  return (
    <div className="absolute inset-0 flex items-center justify-center p-8">
      <div className="grid max-w-sm justify-items-center gap-3 text-center">
        <span className="flex size-11 items-center justify-center rounded-2xl border border-line-strong bg-surface-raised text-muted">{icon}</span>
        <p className="text-[15px] font-medium text-title">{title}</p>
        {text && <p className="text-[13px] leading-relaxed text-muted">{text}</p>}
        {onBrowser && (
          <Button size="sm" variant="secondary" onClick={onBrowser} className="mt-1">
            <ExternalLink />
            {t('chat.artifacts.openInBrowser')}
          </Button>
        )}
      </div>
    </div>
  );
}
