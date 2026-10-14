// The app on a paired phone (internal/daemon/lan.go): the chats, and nothing
// else. A list of every project's chat and its agents', the ones that need you
// first, and a chat a screen, the same ChatTab the desktop app has: the
// conversation live, the composer, and the cards for what an agent asks.
//
// Where you are is the page's fragment (#/p/<project>, #/a/<project>/<agent>,
// #/pr/<project>), so the phone's back gesture goes back to the list.
//
// A phone the user let merge (its "merge" scope, turned on in the app on the
// computer) also gets each project's open pull requests, and merges one only
// after confirming it here: the daemon refuses a phone's merge that doesn't
// say so.
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, ChevronRight, GitPullRequest, MessagesSquare, WifiOff } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import * as T from '../../shared/api';
import { ChatTab } from '../components/chat/ChatTab';
import { ConfirmDialog } from '../components/ConfirmDialog';
import { ProjectChatPanel } from '../components/ProjectChatPanel';
import { AIIcon, LiveAgentAvatar } from '../components/state';
import { chatLabel, rank, type StatusTone } from '../lib/agentStatus';
import { api } from '../lib/api';
import { t, useT } from '../lib/i18n';
import { projectLabel } from '../lib/projectName';
import { useConnection } from '../lib/events';
import { cn, errorMessage } from '../lib/utils';
import { PlainHTTPNote } from './PhonePair';

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

function leadLabel(chat: string | undefined): { text: string; tone: StatusTone } {
  if (chat === T.ChatWaiting) return { text: t('web.phone.needsYou'), tone: 'urgent' };
  if (chat === T.ChatRunning) return { text: t('web.phone.working'), tone: 'live' };
  if (chat === T.ChatError) return { text: t('common.error'), tone: 'error' };
  return { text: chat === T.ChatOff || !chat ? t('web.phone.notStarted') : t('web.phone.idle'), tone: 'muted' };
}

export function PhoneApp({ phone }: { phone: T.LANPhone }) {
  const t = useT();
  const [route, go] = useRoute();
  const connection = useConnection();
  return (
    <div className="flex h-dvh flex-col" data-phone-app>
      {connection.state === 'disconnected' && (
        <div className="flex items-center gap-2 bg-amber-500/15 px-4 py-1.5 text-[12px] text-amber-200" data-phone-offline>
          <WifiOff className="size-3.5 shrink-0" />
          <span className="truncate">{connection.error || t('web.phone.unreachable')}</span>
        </div>
      )}
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

function ChatList({ phone, onOpen }: { phone: T.LANPhone; onOpen: (route: Route) => void }) {
  const t = useT();
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
        <h1 className="text-[17px] font-semibold tracking-tight text-title">{t('web.phone.chats')}</h1>
      </header>
      <div className="grid gap-6 px-3 py-4">
        {projects.isPending && <p className="px-2 text-sm text-subtle">{t('common.loading')}</p>}
        {projects.error && <p className="px-2 text-sm text-rose-300">{errorMessage(projects.error)}</p>}
        {projects.data?.length === 0 && <p className="px-2 text-sm text-subtle">{t('web.phone.noProjects')}</p>}
        {projects.data?.map((p, i) => {
          const lead = chats[i]?.data;
          const label = leadLabel(lead?.chat);
          const own = (agents.data ?? []).filter((a) => a.project === p.name).sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
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
              {(phone.scopes ?? []).includes('merge') && (
                <ChatRow
                  icon={
                    <div className="flex size-9 items-center justify-center rounded-full bg-surface-raised">
                      <GitPullRequest className="size-4 text-secondary" />
                    </div>
                  }
                  title={t('web.phone.pulls')}
                  status={{ text: t('web.phone.pullsHint'), tone: 'muted' }}
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
  const t = useT();
  return (
    <header className="flex shrink-0 items-center gap-1 border-b border-line bg-overlay/90 px-1.5 py-1.5 backdrop-blur">
      <button type="button" aria-label={t('web.phone.back')} onClick={onBack} className="flex size-10 items-center justify-center rounded-full text-secondary active:bg-surface-raised">
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
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const p = projects.data?.find((x) => x.name === project);
  const chat = useQuery({ queryKey: ['projectChat', project], queryFn: () => api.projectChat(project) });
  return (
    <>
      <ScreenHeader title={t('web.phone.projectChat')} subtitle={`${projectLabel(project, projects.data)}${chat.data ? ` · ${leadLabel(chat.data.chat).text}` : ''}`} onBack={onBack} />
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
  const start = useMutation({
    mutationFn: () => api.agentAction(agentRef, agent?.state === 'paused' ? 'resume' : 'start'),
    onError: (err) => toast.error(t('web.phone.couldntStart', { agent: agentRef }), { description: errorMessage(err) }),
  });
  return (
    <>
      <ScreenHeader title={agent?.title || agentRef.split('/')[1]} subtitle={`${agentRef}${agent ? ` · ${chatLabel(agent).text}` : ''}`} onBack={onBack} />
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
