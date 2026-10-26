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
import * as DialogPrimitive from '@radix-ui/react-dialog';
import { useMutation, useQueries, useQuery, useQueryClient, type UseQueryResult } from '@tanstack/react-query';
import { ArrowLeft, Check, ChevronDown, ChevronRight, GitPullRequest, LoaderCircle, MessagesSquare, Wifi, WifiOff } from 'lucide-react';
import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { toast } from 'sonner';
import * as T from '../../shared/api';
import { openAgentEvent } from '../components/chat/AgentPermissionCard';
import { ChatTab } from '../components/chat/ChatTab';
import { Elapsed } from '../components/chat/Timeline';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { ProjectChatPanel } from '../components/ProjectChatPanel';
import { AIIcon, LiveAgentAvatar } from '../components/state';
import { prChecksText, rank, type StatusTone } from '../lib/agentStatus';
import { api } from '../lib/api';
import { chatKey, fetchThread, peekThread } from '../lib/chat';
import { agentChatStatus, lastReply, leadChatStatus, type ChatStatus } from '../lib/chatActivity';
import { useT } from '../lib/i18n';
import { projectLabel } from '../lib/projectName';
import { useConnection } from '../lib/events';
import { cn, errorMessage } from '../lib/utils';
import { reconnectNow } from './bridge';
import { parseRoute, routeHash, type Route } from './route';
import { PlainHTTPNote } from './PhonePair';
import { seen } from './seen';

// Nav is how a screen moves: open pushes a screen, so back (the arrow, or
// the phone's gesture) returns to the one it came from; swap replaces this
// one, as the switcher does, so back still goes where it would have; home
// is the list, by going back as far as the list when that's where we came
// from. Each history entry counts how deep it is, for that.
type Nav = { open: (next: Route) => void; swap: (next: Route) => void; back: () => void; home: () => void };

const depth = () => (typeof history.state?.depth === 'number' ? (history.state.depth as number) : 0);

function useRoute(): [Route, Nav] {
  const [route, setRoute] = useState(() => parseRoute(location.hash));
  // The depth of the entry on screen, for an entry that comes without one.
  const shown = useRef(0);
  useEffect(() => {
    // The bottom of the app's history is always the list, so back and "All
    // chats" end there: a page opened on a chat (a link, a bookmark) gets
    // the list put under it. A reload keeps its entries' depths.
    if (typeof history.state?.depth !== 'number') {
      const hash = location.hash;
      if (parseRoute(hash).kind === 'list') history.replaceState({ depth: 0 }, '', hash);
      else {
        history.replaceState({ depth: 0 }, '', routeHash({ kind: 'list' }));
        history.pushState({ depth: 1 }, '', hash);
      }
    }
    shown.current = depth();
    const onHash = () => {
      // A fragment typed or followed by hand is a screen pushed on this one.
      if (typeof history.state?.depth !== 'number') history.replaceState({ depth: shown.current + 1 }, '', location.hash);
      shown.current = depth();
      setRoute(parseRoute(location.hash));
    };
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);
  const list: Route = { kind: 'list' };
  const nav: Nav = {
    open: (next) => {
      if (routeHash(next) === routeHash(route)) return;
      history.pushState({ depth: depth() + 1 }, '', routeHash(next));
      shown.current = depth();
      setRoute(next);
    },
    swap: (next) => {
      history.replaceState(history.state, '', routeHash(next));
      setRoute(next);
    },
    back: () => {
      if (depth() > 0) history.back();
      else nav.swap(list);
    },
    home: () => {
      if (depth() > 0) history.go(-depth());
      else nav.swap(list);
    },
  };
  return [route, nav];
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
  const [route, nav] = useRoute();
  // A project's chat links to its agent on an agent's permission request,
  // and back from there is back to the project's chat.
  const navRef = useRef(nav);
  navRef.current = nav;
  useEffect(() => {
    const open = (e: Event) => navRef.current.open({ kind: 'agent', ref: (e as CustomEvent<string>).detail });
    window.addEventListener(openAgentEvent, open);
    return () => window.removeEventListener(openAgentEvent, open);
  }, []);
  // The phone's own CSS (styles.css) keys on this, portals included.
  useEffect(() => {
    document.documentElement.dataset.phone = '';
    return () => void delete document.documentElement.dataset.phone;
  }, []);
  const chats = useChats(phone);
  const here = routeHash(route);
  const [switching, setSwitching] = useState(false);
  useEffect(() => setSwitching(false), [here]);
  // The chats that want you somewhere else, for the header to say so: you
  // needn't go back to the list to find out.
  const elsewhere = chats.rows.filter((r) => routeHash(r.route) !== here && (r.status.tone === 'urgent' || r.unread));
  const header: HeaderNav = {
    back: nav.back,
    switcher: () => setSwitching(true),
    waiting: elsewhere.length,
    urgent: elsewhere.some((r) => r.status.tone === 'urgent'),
  };
  const project = route.kind === 'list' ? undefined : route.kind === 'agent' ? route.ref.split('/')[0] : route.project;
  const merge = (phone.scopes ?? []).includes('merge');
  return (
    // The page runs under the notch and the home bar (web.html's
    // viewport-fit=cover), and keeps clear of them itself.
    <div className="flex h-dvh flex-col pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)] pt-[env(safe-area-inset-top)]" data-phone-app>
      <ConnectionBar />
      {route.kind === 'list' ? (
        <ChatList phone={phone} chats={chats} onOpen={nav.open} />
      ) : route.kind === 'project' ? (
        <ProjectChatScreen project={route.project} merge={merge} header={header} onOpen={nav.open} />
      ) : route.kind === 'pulls' ? (
        <PullsScreen project={route.project} highlight={route.pr} header={header} onOpen={nav.open} />
      ) : (
        <AgentChatScreen agentRef={route.ref} merge={merge} header={header} onOpen={nav.open} />
      )}
      <Switcher
        open={switching}
        onOpenChange={setSwitching}
        chats={chats}
        here={here}
        project={project}
        onPick={(next) => {
          setSwitching(false);
          if (next.kind === 'list') nav.home();
          else nav.swap(next);
        }}
      />
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
          <button type="button" className="-my-1 min-h-11 shrink-0 rounded-full px-3 font-medium active:bg-amber-500/20" onClick={reconnectNow} data-phone-retry>
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

// A ChatEntry is one row of the list, and of the switcher: a chat, or a
// project's pull requests.
type ChatEntry = {
  route: Route;
  project: T.Project;
  icon: ReactNode;
  title: string;
  subtitle?: string;
  status: ChatStatus;
  unread: boolean;
  // plain is a row that isn't a chat: its line has no dot.
  plain?: boolean;
  chat?: string;
};

type Chats = {
  projects: UseQueryResult<T.Project[]>;
  // Each project's rows, in the rail's order, which follows the agents'
  // states: not what each is doing this second, or rows would move under
  // your finger.
  groups: { project: T.Project; rows: ChatEntry[] }[];
  rows: ChatEntry[];
};

// useChats is every chat the phone can open, with what each is doing: the
// list and the switcher both read it, from the same queries.
function useChats(phone: T.LANPhone): Chats {
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
  const merge = (phone.scopes ?? []).includes('merge');
  const groups = (projects.data ?? []).map((p, i) => {
    const lead = chats[i]?.data;
    const leadRef = lead?.started ? lead.ref : undefined;
    const leadStatus = leadChatStatus(lead?.chat, threadOf(leadRef));
    const rows: ChatEntry[] = [
      {
        route: { kind: 'project', project: p.name },
        project: p,
        icon: (
          <div className="flex size-9 items-center justify-center rounded-full bg-surface-raised">
            <AIIcon ai="claude" className="size-4" />
          </div>
        ),
        title: t('web.phone.projectChat'),
        status: leadStatus,
        unread: unread(leadRef, leadStatus),
      },
      ...(agents.data ?? [])
        .filter((a) => a.project === p.name)
        .sort((x, y) => rank(x) - rank(y) || x.name.localeCompare(y.name))
        .map((a): ChatEntry => {
          const status = agentChatStatus(a, threadOf(a.ref));
          return {
            route: { kind: 'agent', ref: a.ref },
            project: p,
            icon: <LiveAgentAvatar agent={a} className="size-9" />,
            title: a.title || a.name,
            subtitle: a.title ? a.name : undefined,
            status,
            unread: unread(a.ref, status),
            chat: a.ref,
          };
        }),
    ];
    if (merge) {
      rows.push({
        route: { kind: 'pulls', project: p.name },
        project: p,
        icon: (
          <div className="flex size-9 items-center justify-center rounded-full bg-surface-raised">
            <GitPullRequest className="size-4 text-secondary" />
          </div>
        ),
        title: t('web.phone.pulls'),
        status: { text: t('web.phone.pullsHint'), tone: 'muted', live: false },
        unread: false,
        plain: true,
      });
    }
    return { project: p, rows };
  });
  return { projects, groups, rows: groups.flatMap((g) => g.rows) };
}

// needsYou is the chats waiting on an answer or a permission, from every
// project: the list and the switcher put them first, so none waits below
// the fold.
const needsYou = (rows: ChatEntry[]) => rows.filter((r) => r.status.tone === 'urgent');

// ChatSections is the chats in sections, those that need you first: the
// list's body, and the switcher's.
function ChatSections({ chats, first, here, onOpen }: { chats: Chats; first?: string; here?: string; onOpen: (route: Route) => void }) {
  const t = useT();
  const urgent = needsYou(chats.rows);
  const groups = first ? [...chats.groups.filter((g) => g.project.name === first), ...chats.groups.filter((g) => g.project.name !== first)] : chats.groups;
  const row = (r: ChatEntry, where?: string) => (
    <ChatRow
      key={routeHash(r.route)}
      icon={r.icon}
      title={r.title}
      subtitle={where ? [r.subtitle, where].filter(Boolean).join(' · ') : r.subtitle}
      status={r.status}
      unread={r.unread}
      plain={r.plain}
      current={here === routeHash(r.route)}
      onClick={() => onOpen(r.route)}
      data-phone-chat={r.chat}
    />
  );
  return (
    <>
      {urgent.length > 0 && (
        <section className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1" data-phone-needs-you>
          <h2 className="px-2 pb-1 text-[12px] font-semibold uppercase tracking-[0.12em] text-amber-300">{t('web.phone.needsYou')}</h2>
          {urgent.map((r) => row(r, projectLabel(r.project)))}
        </section>
      )}
      {groups.map(({ project, rows }) => (
        <section key={project.name} className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1" data-phone-project={project.name}>
          <h2 className="px-2 pb-1 text-[12px] font-semibold uppercase tracking-[0.12em] text-subtle">{projectLabel(project)}</h2>
          {rows.filter((r) => !urgent.includes(r)).map((r) => row(r))}
        </section>
      ))}
    </>
  );
}

function ChatList({ phone, chats, onOpen }: { phone: T.LANPhone; chats: Chats; onOpen: (route: Route) => void }) {
  const t = useT();
  const { projects } = chats;
  const offline = useConnection().state !== 'connected';
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
      <div className="grid grid-cols-[minmax(0,1fr)] gap-6 px-3 py-4">
        {projects.isPending && <p className="px-2 text-sm text-subtle">{t('common.loading')}</p>}
        {projects.error &&
          (offline ? (
            // The bar above says it is reconnecting: what's missing is why
            // the list is empty, not the request's own error.
            <div className="grid justify-items-center gap-3 px-6 py-16 text-center" data-phone-unreachable>
              <WifiOff className="size-8 text-faint" />
              <p className="text-sm text-subtle">{t('web.phone.unreachable')}</p>
            </div>
          ) : (
            <p className="px-2 text-sm text-rose-300">{errorMessage(projects.error)}</p>
          ))}
        {projects.data?.length === 0 && <p className="px-2 text-sm text-subtle">{t('web.phone.noProjects')}</p>}
        <ChatSections chats={chats} onOpen={onOpen} />
        <footer className="grid gap-2 border-t border-line px-2 pt-4">
          <p className="text-[12px] text-subtle">
            {t.rich('web.phone.pairedAs', { name: <span className="text-secondary">{phone.name}</span> })}
          </p>
          <PlainHTTPNote />
          <div>
            <button type="button" className="-mx-2 min-h-11 rounded-full px-2 text-[13px] text-rose-300 active:bg-rose-500/10" disabled={unpair.isPending} onClick={() => unpair.mutate()}>
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
  current,
  onClick,
  ...rest
}: {
  icon: ReactNode;
  title: string;
  subtitle?: string;
  status: ChatStatus;
  unread?: boolean;
  plain?: boolean;
  // current is the chat on screen, in the switcher.
  current?: boolean;
  onClick: () => void;
  'data-phone-chat'?: string;
}) {
  const t = useT();
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={current || undefined}
      className={cn(
        'flex min-h-14 w-full items-center gap-3 rounded-2xl px-2.5 py-2 text-left active:bg-surface-raised',
        status.tone === 'urgent' && 'bg-amber-500/10',
        current && 'bg-surface-raised ring-1 ring-line-strong',
      )}
      data-phone-row
      data-phone-live={status.live || undefined}
      data-phone-unread={unread || undefined}
      {...rest}
    >
      <div className="shrink-0">{icon}</div>
      <div className="min-w-0 flex-1">
        <p className={cn('truncate text-[15px] text-primary', unread ? 'font-semibold text-title' : 'font-medium')}>{title}</p>
        {plain ? <p className="truncate text-[12px] text-subtle">{status.text}</p> : <StatusLine status={status} after={subtitle} className="text-[12px]" />}
      </div>
      {unread && (
        <span className="flex shrink-0 items-center gap-1 rounded-full bg-brand-500/15 px-2 py-0.5 text-[11px] font-semibold text-brand-200" data-phone-new>
          <span className="size-1.5 rounded-full bg-brand-400" />
          {t('web.phone.unread')}
        </span>
      )}
      {current ? <Check className="size-4 shrink-0 text-secondary" /> : <ChevronRight className="size-4 shrink-0 text-faint" />}
    </button>
  );
}

// Switcher is every chat in a sheet from the bottom, the one on screen's
// project first, those that need you before that: from any chat to any
// other in two taps, the header's title and the row, without the list.
function Switcher({
  open,
  onOpenChange,
  chats,
  here,
  project,
  onPick,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  chats: Chats;
  here: string;
  project?: string;
  onPick: (route: Route) => void;
}) {
  const t = useT();
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 animate-fade-in bg-scrim backdrop-blur-[2px]" />
        <DialogPrimitive.Content
          aria-describedby={undefined}
          className="fixed inset-x-0 bottom-0 z-50 flex max-h-[85dvh] animate-sheet-up flex-col rounded-t-3xl border-t border-line-strong bg-modal pb-[env(safe-area-inset-bottom)] shadow-[0_-24px_60px_-20px_var(--ab-shadow-deep)] focus:outline-none"
          data-phone-switcher
          // Back on the title that opened it, or the next screen's.
          onCloseAutoFocus={(e) => {
            e.preventDefault();
            document.querySelector<HTMLElement>('[data-phone-switch]')?.focus();
          }}
        >
          <div className="mx-auto mt-2 h-1 w-10 shrink-0 rounded-full bg-line-strong" />
          <div className="flex shrink-0 items-center gap-2 px-4 pb-2 pt-2">
            <DialogPrimitive.Title className="min-w-0 flex-1 truncate text-[17px] font-semibold text-title">{t('web.phone.switch')}</DialogPrimitive.Title>
            <button type="button" onClick={() => onPick({ kind: 'list' })} className="flex min-h-11 items-center gap-1.5 rounded-full px-3 text-[14px] font-medium text-brand-300 active:bg-surface-raised" data-phone-all>
              <MessagesSquare className="size-4" />
              {t('web.phone.allChats')}
            </button>
          </div>
          <div className="grid min-h-0 grid-cols-[minmax(0,1fr)] content-start gap-5 overflow-y-auto overscroll-contain px-3 pb-4 pt-1">
            <ChatSections chats={chats} first={project} here={here} onOpen={onPick} />
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
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

// HeaderNav is what a screen's header moves with: back, the switcher, and
// how many chats elsewhere want you.
type HeaderNav = { back: () => void; switcher: () => void; waiting: number; urgent: boolean };

// ScreenHeader is back, and the screen's title, which opens the switcher:
// the chevron says so, and a count beside it says other chats want you.
// action is a button of the screen's own, on the right.
function ScreenHeader({ title, subtitle, status, header, action }: { title: string; subtitle?: string; status?: ChatStatus; header: HeaderNav; action?: ReactNode }) {
  const t = useT();
  return (
    <header className="flex shrink-0 items-center gap-1 border-b border-line bg-overlay/90 px-1.5 py-1 backdrop-blur">
      <button type="button" aria-label={t('common.back')} onClick={header.back} className="flex size-11 shrink-0 items-center justify-center rounded-full text-secondary active:bg-surface-raised" data-phone-back>
        <ArrowLeft className="size-5" />
      </button>
      <button
        type="button"
        onClick={header.switcher}
        aria-haspopup="dialog"
        aria-label={t('web.phone.switchFrom', { title })}
        className="flex min-h-11 min-w-0 flex-1 items-center gap-2 rounded-xl px-1 text-left active:bg-surface-raised"
        data-phone-switch
      >
        <div className="min-w-0 flex-1">
          <p className="flex min-w-0 items-center gap-1 text-[15px] font-semibold text-title">
            <span className="truncate">{title}</span>
            <ChevronDown className="size-4 shrink-0 text-subtle" />
          </p>
          {status ? <StatusLine status={status} after={subtitle} className="text-[11.5px]" /> : subtitle && <p className="truncate text-[11px] text-subtle">{subtitle}</p>}
        </div>
        {header.waiting > 0 && (
          <span
            className={cn(
              'flex h-6 min-w-6 shrink-0 items-center justify-center rounded-full px-1.5 text-[12px] font-semibold tabular-nums',
              header.urgent ? 'bg-amber-400 text-black' : 'bg-brand-500 text-white',
            )}
            aria-label={t('web.phone.waitingElsewhere', { count: header.waiting })}
            data-phone-waiting={header.waiting}
          >
            {header.waiting}
          </span>
        )}
      </button>
      {action}
    </header>
  );
}

// useOpenPulls is a project's open pull requests, for a phone that may merge.
function useOpenPulls(project: string, enabled: boolean) {
  const pulls = useQuery({ queryKey: ['projectPullRequests', project], queryFn: () => api.projectPullRequests(project), enabled });
  return { ...pulls, open: (pulls.data?.pullRequests ?? []).filter((pr) => pr.state === 'open') };
}

const checksTone = (checks: string) => (checks === 'failing' ? 'text-rose-300' : checks === 'passing' ? 'text-emerald-300' : 'text-subtle');

// PullChip is a header's way to a pull request, or to all of a project's:
// a 44 px target with the icon, the number or count, and the checks' colour.
function PullChip({ label, text, checks, onClick }: { label: string; text: string; checks?: string; onClick: () => void }) {
  return (
    <button type="button" aria-label={label} onClick={onClick} className="flex min-h-11 shrink-0 items-center gap-1 rounded-full px-2.5 text-[13px] font-medium text-secondary active:bg-surface-raised" data-phone-pull-chip>
      <GitPullRequest className={cn('size-4', checks ? checksTone(checks) : 'text-secondary')} />
      <span className="tabular-nums">{text}</span>
    </button>
  );
}

function ProjectChatScreen({ project, merge, header, onOpen }: { project: string; merge: boolean; header: HeaderNav; onOpen: (route: Route) => void }) {
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const p = projects.data?.find((x) => x.name === project);
  const chat = useQuery({ queryKey: ['projectChat', project], queryFn: () => api.projectChat(project) });
  const ref = chat.data?.started ? chat.data.ref : undefined;
  const thread = useThread(ref);
  useMarkRead(ref, thread.data);
  const pulls = useOpenPulls(project, merge);
  return (
    <>
      <ScreenHeader
        title={t('web.phone.projectChat')}
        subtitle={projectLabel(project, projects.data)}
        status={chat.data ? leadChatStatus(chat.data.chat, thread.data) : undefined}
        header={header}
        action={merge && pulls.open.length > 0 && <PullChip label={t('web.phone.pulls')} text={String(pulls.open.length)} onClick={() => onOpen({ kind: 'pulls', project })} />}
      />
      <div className="flex min-h-0 flex-1 flex-col">
        {p ? <ProjectChatPanel project={p} /> : <Missing loading={projects.isPending} message={t('web.phone.missingProject', { name: projectLabel(project, projects.data) })} />}
      </div>
    </>
  );
}

function AgentChatScreen({ agentRef, merge, header, onOpen }: { agentRef: string; merge: boolean; header: HeaderNav; onOpen: (route: Route) => void }) {
  const t = useT();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const agent = agents.data?.find((a) => a.ref === agentRef);
  const [project, name] = agentRef.split('/');
  const thread = useThread(agent ? agentRef : undefined);
  useMarkRead(agent ? agentRef : undefined, thread.data);
  // The agent's own pull request, a tap from its chat.
  const pr = useOpenPulls(project, merge).open.find((x) => x.agent === name);
  const start = useMutation({
    mutationFn: () => api.agentAction(agentRef, agent?.state === 'paused' ? 'resume' : 'start'),
    onError: (err) => toast.error(t('web.phone.couldntStart', { agent: agentRef }), { description: errorMessage(err) }),
  });
  return (
    <>
      <ScreenHeader
        title={agent?.title || name}
        subtitle={agent?.title ? agent.name : undefined}
        status={agent ? agentChatStatus(agent, thread.data) : undefined}
        header={header}
        action={pr && <PullChip label={t('web.phone.openPull', { number: pr.number })} text={`#${pr.number}`} checks={pr.checks} onClick={() => onOpen({ kind: 'pulls', project, pr: pr.number })} />}
      />
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

function PullsScreen({ project, highlight, header, onOpen }: { project: string; highlight?: number; header: HeaderNav; onOpen: (route: Route) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const pulls = useOpenPulls(project, true);
  const [merging, setMerging] = useState<T.PullRequest | null>(null);
  const open = pulls.open;
  // The pull request opened from its agent's chat, in view once the list is.
  const loaded = !!pulls.data;
  useEffect(() => {
    if (loaded && highlight) document.querySelector(`[data-phone-pull="${highlight}"]`)?.scrollIntoView({ block: 'nearest' });
  }, [loaded, highlight]);
  const method = pulls.data?.mergeMethods?.[0] ?? 'merge';
  // The dialog closes and the row leaves the moment the merge is confirmed;
  // if GitHub refuses, the row comes back and a toast says why.
  const key = ['projectPullRequests', project];
  const withState = (number: number, state: string) =>
    queryClient.setQueryData<T.ProjectPullRequests>(key, (d) => d && { ...d, pullRequests: d.pullRequests.map((p) => (p.number === number ? { ...p, state } : p)) });
  const merge = useMutation({
    mutationFn: (number: number) => api.mergePullRequest(project, number, method, true),
    onMutate: async (number) => {
      await queryClient.cancelQueries({ queryKey: key });
      withState(number, 'merged');
    },
    onSuccess: (_pr, number) => toast.success(t('web.phone.merged', { number })),
    onError: (err, number) => {
      withState(number, 'open');
      toast.error(t('memory.pulls.mergeFailed', { error: errorMessage(err) }));
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: key }),
  });
  return (
    <>
      <ScreenHeader title={t('web.phone.pulls')} subtitle={projectLabel(project, projects.data)} header={header} />
      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3" data-phone-pulls>
        {pulls.isPending && <p className="px-2 text-sm text-subtle">{t('common.loading')}</p>}
        {pulls.error && <p className="px-2 text-sm text-rose-300">{errorMessage(pulls.error)}</p>}
        {pulls.data && open.length === 0 && <p className="px-2 text-sm text-subtle">{t('web.phone.noPulls')}</p>}
        <ul className="grid gap-2">
          {open.map((pr) => {
            const details = (
              <>
                <p className="line-clamp-2 break-words text-[14px] font-medium text-primary">{pr.title}</p>
                <p className="flex min-w-0 items-center gap-1 text-[12px] text-subtle">
                  <span className="truncate">
                    #{pr.number}
                    {pr.baseBranch && ` → ${pr.baseBranch}`}
                    {pr.checks && (
                      <>
                        {' · '}
                        <span className={checksTone(pr.checks)}>{prChecksText(pr.checks)}</span>
                      </>
                    )}
                    {pr.draft && ` · ${t('web.phone.draft')}`}
                    {pr.agent && ` · ${pr.agent}`}
                  </span>
                  {pr.agent && <ChevronRight className="size-3.5 shrink-0 text-faint" />}
                </p>
              </>
            );
            return (
              <li
                key={pr.number}
                className={cn('flex min-w-0 items-center gap-2 rounded-2xl border border-line py-1.5 pl-1.5 pr-3', pr.number === highlight && 'border-brand-400/60 bg-brand-500/10')}
                data-phone-pull={pr.number}
              >
                {/* The pull request's agent, a tap away: what it did, and
                    what it says about it, before merging. */}
                {pr.agent ? (
                  <button
                    type="button"
                    className="min-w-0 flex-1 rounded-xl px-1.5 py-1 text-left active:bg-surface-raised"
                    aria-label={t('web.phone.openAgent', { agent: pr.agent })}
                    onClick={() => onOpen({ kind: 'agent', ref: `${project}/${pr.agent}` })}
                    data-phone-pull-agent
                  >
                    {details}
                  </button>
                ) : (
                  <div className="min-w-0 flex-1 px-1.5 py-1">{details}</div>
                )}
                <button
                  type="button"
                  disabled={pr.draft}
                  onClick={() => setMerging(pr)}
                  className="min-h-11 shrink-0 rounded-full bg-brand-500/20 px-4 text-[14px] font-medium text-brand-200 active:bg-brand-500/30 disabled:opacity-40"
                  data-phone-merge
                >
                  {t('web.phone.merge')}
                </button>
              </li>
            );
          })}
        </ul>
      </div>
      <ConfirmDialog
        open={merging !== null}
        onOpenChange={(o) => !o && setMerging(null)}
        title={merging ? t('web.phone.mergeTitle', { number: merging.number, base: merging.baseBranch || '—' }) : ''}
        description={
          merging && (
            <>
              {merging.title}
              {(merging.checks === 'failing' || merging.checks === 'pending') && (
                <span className={cn('mt-2 block font-medium', merging.checks === 'failing' ? 'text-rose-300' : 'text-amber-300')} data-phone-merge-warning={merging.checks}>
                  {t(merging.checks === 'failing' ? 'web.phone.mergeFailing' : 'web.phone.mergePending')}
                </span>
              )}
            </>
          )
        }
        confirmLabel={t('web.phone.merge')}
        destructive
        onConfirm={async () => {
          if (merging) merge.mutate(merging.number);
        }}
      />
    </>
  );
}

function Missing({ loading, message }: { loading: boolean; message: string }) {
  const t = useT();
  return <div className="flex flex-1 items-center justify-center px-6 text-center text-sm text-subtle">{loading ? t('common.loading') : message}</div>;
}
