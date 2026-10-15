// The app on a paired phone (internal/daemon/lan.go): the chats, and nothing
// else. A list of every project's chat and its agents', the ones that need you
// first, and a chat a screen, the same ChatTab the desktop app has: the
// conversation live, the composer, and the cards for what an agent asks.
//
// Where you are is the page's fragment (#/p/<project>, #/a/<project>/<agent>,
// #/pr/<project>), so the phone's back gesture goes back to the list.
//
// Each chat says what it is doing as it does it (lib/chatActivity.ts): the
// tool call it runs, thinking, writing, a permission to give, with how long
// its turn has taken, and how its last turn left it otherwise. The list reads
// only each chat's latest turn for that (peekThread), and a chat that said
// something since this phone last showed it is marked new (seen.ts).
//
// When the phone sleeps or the tunnel drops, the stream of events is followed
// again (reconnect.ts) under a "Reconnecting…" bar, and everything is read
// again once it's back, so nothing sent meanwhile goes missing.
//
// A phone the user let merge (its "merge" scope, turned on in the app on the
// computer) also gets each project's open pull requests, and merges one only
// after confirming it here: the daemon refuses a phone's merge that doesn't
// say so.
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, ChevronRight, GitPullRequest, LoaderCircle, MessagesSquare, Wifi, WifiOff } from 'lucide-react';
import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { toast } from 'sonner';
import * as T from '../../shared/api';
import { ChatTab } from '../components/chat/ChatTab';
import { Elapsed } from '../components/chat/Timeline';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { ProjectChatPanel } from '../components/ProjectChatPanel';
import { AIIcon, LiveAgentAvatar } from '../components/state';
import { rank, type StatusTone } from '../lib/agentStatus';
import { api } from '../lib/api';
import { chatKey, fetchThread, peekThread } from '../lib/chat';
import { agentChatStatus, lastReply, leadChatStatus, type ChatStatus } from '../lib/chatActivity';
import { useT } from '../lib/i18n';
import { projectLabel } from '../lib/projectName';
import { useConnection } from '../lib/events';
import { cn, errorMessage } from '../lib/utils';
import { reconnectNow } from './bridge';
import { PlainHTTPNote } from './PhonePair';
import { seen } from './seen';

type Route = { kind: 'list' } | { kind: 'project'; project: string } | { kind: 'agent'; ref: string } | { kind: 'pulls'; project: string };

function parseRoute(hash: string): Route {
  const parts = hash.replace(/^#\/?/, '').split('/').map(decodeURIComponent);
  if (parts[0] === 'p' && parts[1]) return { kind: 'project', project: parts[1] };
  if (parts[0] === 'a' && parts[1] && parts[2]) return { kind: 'agent', ref: `${parts[1]}/${parts[2]}` };
  if (parts[0] === 'pr' && parts[1]) return { kind: 'pulls', project: parts[1] };
  return { kind: 'list' };
}

function routeHash(route: Route): string {
  if (route.kind === 'project') return `#/p/${encodeURIComponent(route.project)}`;
  if (route.kind === 'pulls') return `#/pr/${encodeURIComponent(route.project)}`;
  if (route.kind === 'agent') return `#/a/${route.ref.split('/').map(encodeURIComponent).join('/')}`;
  return '#/';
}

function useRoute(): [Route, (next: Route) => void] {
  const [route, setRoute] = useState(() => parseRoute(location.hash));
  useEffect(() => {
    const onHash = () => setRoute(parseRoute(location.hash));
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);
  const go = (next: Route) => {
    // Back to the list is going back, when the list is where we came from.
    if (next.kind === 'list' && history.state?.fromList) history.back();
    else {
      history.pushState({ fromList: route.kind === 'list' }, '', routeHash(next));
      setRoute(next);
    }
  };
  return [route, go];
}

const toneText: Record<StatusTone, string> = {
  urgent: 'text-amber-300',
  error: 'text-rose-300',
  warning: 'text-amber-300/80',
  live: 'text-emerald-300',
  muted: 'text-subtle',
};

const toneDot: Record<StatusTone, string> = {
  urgent: 'bg-amber-400',
  error: 'bg-rose-400',
  warning: 'bg-amber-300/80',
  live: 'bg-emerald-400',
  muted: 'bg-faint',
};

// The chats whose conversation can be read: a lead once started, and an
// agent whose machine is, or was, up.
const readable = (a: T.Agent) => a.state === 'running' || a.state === 'stopped' || a.state === 'paused';

// useThread is a chat's thread, as its screen reads it.
function useThread(ref: string | undefined) {
  const queryClient = useQueryClient();
  return useQuery({ queryKey: chatKey(ref ?? ''), queryFn: () => fetchThread(queryClient, ref!), enabled: !!ref });
}

// useMarkRead marks a chat read while it is on screen, up to what it last said.
function useMarkRead(ref: string | undefined, thread: T.ChatThread | undefined) {
  const reply = lastReply(thread);
  useEffect(() => {
    if (!ref || document.visibilityState === 'hidden') return;
    seen.read(ref, reply && Date.parse(reply) > Date.now() ? reply : new Date().toISOString());
  }, [ref, reply]);
}

export function PhoneApp({ phone }: { phone: T.LANPhone }) {
  const [route, go] = useRoute();
  return (
    <div className="flex h-dvh flex-col" data-phone-app>
      <ConnectionBar />
      {route.kind === 'list' ? (
        <ChatList phone={phone} onOpen={go} />
      ) : route.kind === 'project' ? (
        <ProjectChatScreen project={route.project} onBack={() => go({ kind: 'list' })} />
      ) : route.kind === 'pulls' ? (
        <PullsScreen project={route.project} onBack={() => go({ kind: 'list' })} />
      ) : (
        <AgentChatScreen agentRef={route.ref} onBack={() => go({ kind: 'list' })} />
      )}
    </div>
  );
}

// ConnectionBar says the phone lost AgentBox, and is getting it back, until
// it is: the page loading for the first time says nothing, and getting back
// says so for a moment.
function ConnectionBar() {
  const t = useT();
  const connection = useConnection();
  const [back, setBack] = useState(false);
  const ever = useRef(false);
  const wasLost = useRef(false);
  useEffect(() => {
    if (connection.state === 'connected') {
      ever.current = true;
      if (!wasLost.current) return;
      wasLost.current = false;
      setBack(true);
      const timer = setTimeout(() => setBack(false), 2_000);
      return () => clearTimeout(timer);
    }
    if (ever.current || connection.state === 'disconnected') {
      wasLost.current = true;
      setBack(false);
    }
  }, [connection.state]);
  const lost = connection.state === 'disconnected' || (ever.current && connection.state === 'connecting');
  if (lost) {
    return (
      <div className="flex min-h-9 items-center gap-2 bg-amber-500/15 px-4 py-1 text-[12px] text-amber-200" role="status" data-phone-offline={connection.state}>
        <WifiOff className="size-3.5 shrink-0" />
        <span className="min-w-0 flex-1 truncate font-medium" title={connection.error || t('web.phone.unreachable')}>
          {t('web.phone.reconnecting')}
        </span>
        <LoaderCircle className="size-3.5 shrink-0 animate-spin" />
        {connection.state === 'disconnected' && (
          <button type="button" className="shrink-0 rounded-full px-2 py-1 font-medium underline-offset-2 active:bg-amber-500/20" onClick={reconnectNow} data-phone-retry>
            {t('web.phone.retryNow')}
          </button>
        )}
      </div>
    );
  }
  if (back) {
    return (
      <div className="flex min-h-9 animate-fade-in items-center gap-2 bg-emerald-500/15 px-4 py-1 text-[12px] text-emerald-200" role="status" data-phone-online>
        <Wifi className="size-3.5 shrink-0" />
        <span className="truncate font-medium">{t('web.phone.backOnline')}</span>
      </div>
    );
  }
  return null;
}

function ChatList({ phone, onOpen }: { phone: T.LANPhone; onOpen: (route: Route) => void }) {
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const chats = useQueries({
    queries: (projects.data ?? []).map((p) => ({ queryKey: ['projectChat', p.name], queryFn: () => api.projectChat(p.name) })),
  });
  // Each chat's latest turn, for what it is doing and when it last answered.
  const queryClient = useQueryClient();
  const refs = [...chats.flatMap((c) => (c.data?.started ? [c.data.ref] : [])), ...(agents.data ?? []).filter(readable).map((a) => a.ref)];
  const threads = useQueries({
    queries: refs.map((ref) => ({ queryKey: chatKey(ref), queryFn: () => peekThread(queryClient, ref), staleTime: Infinity })),
  });
  const threadOf = (ref: string | undefined) => (ref ? threads[refs.indexOf(ref)]?.data : undefined);
  useSyncExternalStore(seen.subscribe, seen.version);
  const unread = (ref: string | undefined, status: ChatStatus) => !!ref && !status.live && seen.unread(ref, lastReply(threadOf(ref)));
  const unpair = useMutation({
    mutationFn: async () => {
      const res = await fetch('/lan/unpair', { method: 'POST', credentials: 'same-origin' });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
    },
    onSuccess: () => location.replace('/'),
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <header className="sticky top-0 z-10 flex items-center gap-2.5 border-b border-line bg-overlay/90 px-4 py-3 backdrop-blur">
        <MessagesSquare className="size-5 text-brand-300" />
        <h1 className="text-[17px] font-semibold tracking-tight text-title">{t('web.phone.chats')}</h1>
      </header>
      <div className="grid gap-6 px-3 py-4">
        {projects.isPending && <p className="px-2 text-sm text-subtle">{t('common.loading')}</p>}
        {projects.error && <p className="px-2 text-sm text-rose-300">{errorMessage(projects.error)}</p>}
        {projects.data?.length === 0 && <p className="px-2 text-sm text-subtle">{t('web.phone.noProjects')}</p>}
        {projects.data?.map((p, i) => {
          const lead = chats[i]?.data;
          const leadRef = lead?.started ? lead.ref : undefined;
          const label = leadChatStatus(lead?.chat, threadOf(leadRef));
          // In the rail's order, which follows the agents' states: not what
          // each is doing this second, or rows would move under your finger.
          const own = (agents.data ?? [])
            .filter((a) => a.project === p.name)
            .map((a) => ({ a, status: agentChatStatus(a, threadOf(a.ref)) }))
            .sort((x, y) => rank(x.a) - rank(y.a) || x.a.name.localeCompare(y.a.name));
          return (
            <section key={p.name} className="grid gap-1" data-phone-project={p.name}>
              <h2 className="px-2 pb-1 text-[11px] font-semibold uppercase tracking-[0.12em] text-faint">{projectLabel(p)}</h2>
              <ChatRow
                icon={
                  <div className="flex size-9 items-center justify-center rounded-full bg-surface-raised">
                    <AIIcon ai="claude" className="size-4" />
                  </div>
                }
                title={t('web.phone.projectChat')}
                status={label}
                unread={unread(leadRef, label)}
                onClick={() => onOpen({ kind: 'project', project: p.name })}
              />
              {own.map(({ a, status }) => (
                <ChatRow
                  key={a.ref}
                  icon={<LiveAgentAvatar agent={a} className="size-9" />}
                  title={a.title || a.name}
                  subtitle={a.title ? a.name : undefined}
                  status={status}
                  unread={unread(a.ref, status)}
                  onClick={() => onOpen({ kind: 'agent', ref: a.ref })}
                  data-phone-chat={a.ref}
                />
              ))}
              {(phone.scopes ?? []).includes('merge') && (
                <ChatRow
                  icon={
                    <div className="flex size-9 items-center justify-center rounded-full bg-surface-raised">
                      <GitPullRequest className="size-4 text-secondary" />
                    </div>
                  }
                  title={t('web.phone.pulls')}
                  status={{ text: t('web.phone.pullsHint'), tone: 'muted', live: false }}
                  plain
                  onClick={() => onOpen({ kind: 'pulls', project: p.name })}
                />
              )}
            </section>
          );
        })}
        <footer className="grid gap-2 border-t border-line px-2 pt-4">
          <p className="text-[12px] text-subtle">
            {t.rich('web.phone.pairedAs', { name: <span className="text-secondary">{phone.name}</span> })}
          </p>
          <PlainHTTPNote />
          <div>
            <button type="button" className="text-[12px] text-rose-300 underline-offset-2 hover:underline" disabled={unpair.isPending} onClick={() => unpair.mutate()}>
              {t('web.phone.unpair')}
            </button>
          </div>
        </footer>
      </div>
    </div>
  );
}

function ChatRow({
  icon,
  title,
  subtitle,
  status,
  unread,
  plain,
  onClick,
  ...rest
}: {
  icon: ReactNode;
  title: string;
  subtitle?: string;
  status: ChatStatus;
  unread?: boolean;
  // plain is a row that isn't a chat: its line has no dot.
  plain?: boolean;
  onClick: () => void;
  'data-phone-chat'?: string;
}) {
  const t = useT();
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'flex min-h-14 w-full items-center gap-3 rounded-2xl px-2.5 py-2 text-left active:bg-surface-raised',
        status.tone === 'urgent' && 'bg-amber-500/10',
      )}
      data-phone-row
      data-phone-live={status.live || undefined}
      data-phone-unread={unread || undefined}
      {...rest}
    >
      <div className="shrink-0">{icon}</div>
      <div className="min-w-0 flex-1">
        <p className={cn('truncate text-[15px] text-primary', unread ? 'font-semibold text-title' : 'font-medium')}>{title}</p>
        {plain ? (
          <p className="truncate text-[12px] text-subtle">{status.text}</p>
        ) : (
          <StatusLine status={status} after={subtitle} className="text-[12px]" />
        )}
      </div>
      {unread && (
        <span className="flex shrink-0 items-center gap-1 rounded-full bg-brand-500/15 px-2 py-0.5 text-[11px] font-semibold text-brand-200" data-phone-new>
          <span className="size-1.5 rounded-full bg-brand-400" />
          {t('web.phone.unread')}
        </span>
      )}
      <ChevronRight className="size-4 shrink-0 text-faint" />
    </button>
  );
}

// StatusLine is a chat's status in one line: a dot that pulses while it is
// live, what it is doing, the command it runs, and for how long. after is
// what follows, fainter: the agent's name, or its project.
export function StatusLine({ status, after, className }: { status: ChatStatus; after?: string; className?: string }) {
  return (
    <p className={cn('flex min-w-0 items-center gap-1.5', className)} data-phone-status={status.tone}>
      <span className="relative flex size-2 shrink-0 items-center justify-center">
        {status.live && <span className={cn('absolute inline-flex size-full animate-ping rounded-full opacity-60', toneDot[status.tone])} />}
        <span className={cn('relative inline-flex size-1.5 rounded-full', toneDot[status.tone])} />
      </span>
      <span className="flex min-w-0 items-baseline gap-1 truncate">
        <span className={cn('shrink-0', toneText[status.tone])}>{status.text}</span>
        {status.command && <code className="min-w-0 truncate rounded bg-surface-raised px-1 font-mono text-[0.92em] text-secondary">{status.command}</code>}
        {status.since && (
          <span className="shrink-0 tabular-nums text-subtle">
            · <Elapsed since={status.since} />
          </span>
        )}
        {after && <span className="min-w-0 truncate text-faint">· {after}</span>}
      </span>
    </p>
  );
}

function ScreenHeader({ title, subtitle, status, onBack }: { title: string; subtitle?: string; status?: ChatStatus; onBack: () => void }) {
  const t = useT();
  return (
    <header className="flex shrink-0 items-center gap-1 border-b border-line bg-overlay/90 px-1.5 py-1.5 backdrop-blur">
      <button type="button" aria-label={t('web.phone.back')} onClick={onBack} className="flex size-10 items-center justify-center rounded-full text-secondary active:bg-surface-raised">
        <ArrowLeft className="size-5" />
      </button>
      <div className="min-w-0 flex-1">
        <p className="truncate text-[15px] font-semibold text-title">{title}</p>
        {status ? <StatusLine status={status} after={subtitle} className="text-[11.5px]" /> : subtitle && <p className="truncate text-[11px] text-subtle">{subtitle}</p>}
      </div>
    </header>
  );
}

function ProjectChatScreen({ project, onBack }: { project: string; onBack: () => void }) {
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const p = projects.data?.find((x) => x.name === project);
  const chat = useQuery({ queryKey: ['projectChat', project], queryFn: () => api.projectChat(project) });
  const ref = chat.data?.started ? chat.data.ref : undefined;
  const thread = useThread(ref);
  useMarkRead(ref, thread.data);
  return (
    <>
      <ScreenHeader
        title={t('web.phone.projectChat')}
        subtitle={projectLabel(project, projects.data)}
        status={chat.data ? leadChatStatus(chat.data.chat, thread.data) : undefined}
        onBack={onBack}
      />
      <div className="flex min-h-0 flex-1 flex-col">
        {p ? <ProjectChatPanel project={p} /> : <Missing loading={projects.isPending} message={t('web.phone.missingProject', { name: projectLabel(project, projects.data) })} />}
      </div>
    </>
  );
}

function AgentChatScreen({ agentRef, onBack }: { agentRef: string; onBack: () => void }) {
  const t = useT();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const agent = agents.data?.find((a) => a.ref === agentRef);
  const thread = useThread(agent ? agentRef : undefined);
  useMarkRead(agent ? agentRef : undefined, thread.data);
  const start = useMutation({
    mutationFn: () => api.agentAction(agentRef, agent?.state === 'paused' ? 'resume' : 'start'),
    onError: (err) => toast.error(t('web.phone.couldntStart', { agent: agentRef }), { description: errorMessage(err) }),
  });
  return (
    <>
      <ScreenHeader title={agent?.title || agentRef.split('/')[1]} subtitle={agent?.title ? agent.name : undefined} status={agent ? agentChatStatus(agent, thread.data) : undefined} onBack={onBack} />
      <div className="flex min-h-0 flex-1 flex-col">
        {agent ? (
          <ChatTab agent={agent} starting={start.isPending} onStart={() => start.mutate()} />
        ) : (
          <Missing loading={agents.isPending} message={t('web.phone.missingAgent', { name: agentRef })} />
        )}
      </div>
    </>
  );
}

function PullsScreen({ project, onBack }: { project: string; onBack: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const pulls = useQuery({ queryKey: ['projectPullRequests', project], queryFn: () => api.projectPullRequests(project) });
  const [merging, setMerging] = useState<T.PullRequest | null>(null);
  const open = (pulls.data?.pullRequests ?? []).filter((pr) => pr.state === 'open');
  const method = pulls.data?.mergeMethods?.[0] ?? 'merge';
  return (
    <>
      <ScreenHeader title={t('web.phone.pulls')} subtitle={projectLabel(project, projects.data)} onBack={onBack} />
      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3" data-phone-pulls>
        {pulls.isPending && <p className="px-2 text-sm text-subtle">{t('common.loading')}</p>}
        {pulls.error && <p className="px-2 text-sm text-rose-300">{errorMessage(pulls.error)}</p>}
        {pulls.data && open.length === 0 && <p className="px-2 text-sm text-subtle">{t('web.phone.noPulls')}</p>}
        <ul className="grid gap-2">
          {open.map((pr) => (
            <li key={pr.number} className="flex min-w-0 items-center gap-3 rounded-2xl border border-line px-3 py-2.5" data-phone-pull={pr.number}>
              <div className="min-w-0 flex-1">
                <p className="truncate text-[14px] font-medium text-primary">{pr.title}</p>
                <p className="truncate text-[12px] text-subtle">
                  #{pr.number}
                  {pr.baseBranch && ` → ${pr.baseBranch}`}
                  {pr.checks && ` · ${pr.checks}`}
                  {pr.draft && ` · ${t('web.phone.draft')}`}
                </p>
              </div>
              <button
                type="button"
                disabled={pr.draft}
                onClick={() => setMerging(pr)}
                className="shrink-0 rounded-full bg-brand-500/20 px-3 py-1.5 text-[13px] font-medium text-brand-200 active:bg-brand-500/30 disabled:opacity-40"
              >
                {t('web.phone.merge')}
              </button>
            </li>
          ))}
        </ul>
      </div>
      <ConfirmDialog
        open={merging !== null}
        onOpenChange={(o) => !o && setMerging(null)}
        title={merging ? t('web.phone.mergeTitle', { number: merging.number, base: merging.baseBranch || '—' }) : ''}
        description={merging?.title}
        confirmLabel={t('web.phone.merge')}
        destructive
        onConfirm={async () => {
          if (!merging) return;
          await api.mergePullRequest(project, merging.number, method, true);
          toast.success(t('web.phone.merged', { number: merging.number }));
          void queryClient.invalidateQueries({ queryKey: ['projectPullRequests', project] });
        }}
      />
    </>
  );
}

function Missing({ loading, message }: { loading: boolean; message: string }) {
  const t = useT();
  return <div className="flex flex-1 items-center justify-center px-6 text-center text-sm text-subtle">{loading ? t('common.loading') : message}</div>;
}
