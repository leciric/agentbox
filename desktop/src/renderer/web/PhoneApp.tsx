// The app on a paired phone (internal/daemon/lan.go): the chats, and nothing
// else. A list of every project's chat and its agents', the ones that need you
// first, and a chat a screen, the same ChatTab the desktop app has: the
// conversation live, the composer, and the cards for what an agent asks.
//
// Where you are is the page's fragment (#/p/<project>, #/a/<project>/<agent>),
// so the phone's back gesture goes back to the list.
import { useMutation, useQueries, useQuery } from '@tanstack/react-query';
import { ArrowLeft, ChevronRight, MessagesSquare, WifiOff } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import * as T from '../../shared/api';
import { ChatTab } from '../components/chat/ChatTab';
import { ProjectChatPanel } from '../components/ProjectChatPanel';
import { AIIcon, LiveAgentAvatar } from '../components/state';
import { chatLabel, rank, type StatusTone } from '../lib/agentStatus';
import { api } from '../lib/api';
import { useConnection } from '../lib/events';
import { cn, errorMessage } from '../lib/utils';
import { PlainHTTPNote } from './PhonePair';

type Route = { kind: 'list' } | { kind: 'project'; project: string } | { kind: 'agent'; ref: string };

function parseRoute(hash: string): Route {
  const parts = hash.replace(/^#\/?/, '').split('/').map(decodeURIComponent);
  if (parts[0] === 'p' && parts[1]) return { kind: 'project', project: parts[1] };
  if (parts[0] === 'a' && parts[1] && parts[2]) return { kind: 'agent', ref: `${parts[1]}/${parts[2]}` };
  return { kind: 'list' };
}

function routeHash(route: Route): string {
  if (route.kind === 'project') return `#/p/${encodeURIComponent(route.project)}`;
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
  live: 'text-emerald-300',
  muted: 'text-subtle',
};

function leadLabel(chat: string | undefined): { text: string; tone: StatusTone } {
  if (chat === T.ChatWaiting) return { text: 'Needs you', tone: 'urgent' };
  if (chat === T.ChatRunning) return { text: 'Working', tone: 'live' };
  if (chat === T.ChatError) return { text: 'Error', tone: 'error' };
  return { text: chat === T.ChatOff || !chat ? 'Not started' : 'Idle', tone: 'muted' };
}

export function PhoneApp({ phone }: { phone: T.LANPhone }) {
  const [route, go] = useRoute();
  const connection = useConnection();
  return (
    <div className="flex h-dvh flex-col" data-phone-app>
      {connection.state === 'disconnected' && (
        <div className="flex items-center gap-2 bg-amber-500/15 px-4 py-1.5 text-[12px] text-amber-200" data-phone-offline>
          <WifiOff className="size-3.5 shrink-0" />
          <span className="truncate">{connection.error || "AgentBox on your computer isn't reachable"}</span>
        </div>
      )}
      {route.kind === 'list' ? (
        <ChatList phone={phone} onOpen={go} />
      ) : route.kind === 'project' ? (
        <ProjectChatScreen project={route.project} onBack={() => go({ kind: 'list' })} />
      ) : (
        <AgentChatScreen agentRef={route.ref} onBack={() => go({ kind: 'list' })} />
      )}
    </div>
  );
}

function ChatList({ phone, onOpen }: { phone: T.LANPhone; onOpen: (route: Route) => void }) {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const chats = useQueries({
    queries: (projects.data ?? []).map((p) => ({ queryKey: ['projectChat', p.name], queryFn: () => api.projectChat(p.name) })),
  });
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
        <h1 className="text-[17px] font-semibold tracking-tight text-title">Chats</h1>
      </header>
      <div className="grid gap-6 px-3 py-4">
        {projects.isPending && <p className="px-2 text-sm text-subtle">Loading…</p>}
        {projects.error && <p className="px-2 text-sm text-rose-300">{errorMessage(projects.error)}</p>}
        {projects.data?.length === 0 && <p className="px-2 text-sm text-subtle">There are no projects yet: add one in AgentBox on your computer.</p>}
        {projects.data?.map((p, i) => {
          const lead = chats[i]?.data;
          const label = leadLabel(lead?.chat);
          const own = (agents.data ?? []).filter((a) => a.project === p.name).sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
          return (
            <section key={p.name} className="grid gap-1" data-phone-project={p.name}>
              <h2 className="px-2 pb-1 text-[11px] font-semibold uppercase tracking-[0.12em] text-faint">{p.name}</h2>
              <ChatRow
                icon={
                  <div className="flex size-9 items-center justify-center rounded-full bg-surface-raised">
                    <AIIcon ai="claude" className="size-4" />
                  </div>
                }
                title="Project chat"
                status={label}
                onClick={() => onOpen({ kind: 'project', project: p.name })}
              />
              {own.map((a) => (
                <ChatRow
                  key={a.ref}
                  icon={<LiveAgentAvatar agent={a} className="size-9" />}
                  title={a.title || a.name}
                  subtitle={a.title ? a.name : undefined}
                  status={chatLabel(a)}
                  onClick={() => onOpen({ kind: 'agent', ref: a.ref })}
                />
              ))}
            </section>
          );
        })}
        <footer className="grid gap-2 border-t border-line px-2 pt-4">
          <p className="text-[12px] text-subtle">
            This phone is paired as <span className="text-secondary">{phone.name}</span>.
          </p>
          <PlainHTTPNote />
          <div>
            <button type="button" className="text-[12px] text-rose-300 underline-offset-2 hover:underline" disabled={unpair.isPending} onClick={() => unpair.mutate()}>
              Unpair this phone
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
  onClick,
}: {
  icon: ReactNode;
  title: string;
  subtitle?: string;
  status: { text: string; tone: StatusTone };
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'flex min-h-14 w-full items-center gap-3 rounded-2xl px-2.5 py-2 text-left active:bg-surface-raised',
        status.tone === 'urgent' && 'bg-amber-500/10',
      )}
    >
      <div className="shrink-0">{icon}</div>
      <div className="min-w-0 flex-1">
        <p className="truncate text-[15px] font-medium text-primary">{title}</p>
        <p className="truncate text-[12px]">
          <span className={toneText[status.tone]}>{status.text}</span>
          {subtitle && <span className="text-faint"> · {subtitle}</span>}
        </p>
      </div>
      <ChevronRight className="size-4 shrink-0 text-faint" />
    </button>
  );
}

function ScreenHeader({ title, subtitle, onBack }: { title: string; subtitle?: string; onBack: () => void }) {
  return (
    <header className="flex shrink-0 items-center gap-1 border-b border-line bg-overlay/90 px-1.5 py-1.5 backdrop-blur">
      <button type="button" aria-label="Back to the chats" onClick={onBack} className="flex size-10 items-center justify-center rounded-full text-secondary active:bg-surface-raised">
        <ArrowLeft className="size-5" />
      </button>
      <div className="min-w-0 flex-1">
        <p className="truncate text-[15px] font-semibold text-title">{title}</p>
        {subtitle && <p className="truncate text-[11px] text-subtle">{subtitle}</p>}
      </div>
    </header>
  );
}

function ProjectChatScreen({ project, onBack }: { project: string; onBack: () => void }) {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const p = projects.data?.find((x) => x.name === project);
  const chat = useQuery({ queryKey: ['projectChat', project], queryFn: () => api.projectChat(project) });
  return (
    <>
      <ScreenHeader title="Project chat" subtitle={`${project}${chat.data ? ` · ${leadLabel(chat.data.chat).text}` : ''}`} onBack={onBack} />
      <div className="flex min-h-0 flex-1 flex-col">
        {p ? <ProjectChatPanel project={p} /> : <Missing loading={projects.isPending} what={`the project ${project}`} />}
      </div>
    </>
  );
}

function AgentChatScreen({ agentRef, onBack }: { agentRef: string; onBack: () => void }) {
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const agent = agents.data?.find((a) => a.ref === agentRef);
  const start = useMutation({
    mutationFn: () => api.agentAction(agentRef, agent?.state === 'paused' ? 'resume' : 'start'),
    onError: (err) => toast.error(`Couldn't start ${agentRef}`, { description: errorMessage(err) }),
  });
  return (
    <>
      <ScreenHeader title={agent?.title || agentRef.split('/')[1]} subtitle={`${agentRef}${agent ? ` · ${chatLabel(agent).text}` : ''}`} onBack={onBack} />
      <div className="flex min-h-0 flex-1 flex-col">
        {agent ? (
          <ChatTab agent={agent} starting={start.isPending} onStart={() => start.mutate()} />
        ) : (
          <Missing loading={agents.isPending} what={agentRef} />
        )}
      </div>
    </>
  );
}

function Missing({ loading, what }: { loading: boolean; what: string }) {
  return <div className="flex flex-1 items-center justify-center px-6 text-center text-sm text-subtle">{loading ? 'Loading…' : `There's no ${what} any more.`}</div>;
}
