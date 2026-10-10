import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowDown, EyeOff, LoaderCircle, MessageSquarePlus, RefreshCw } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import { api, isHomeChat, isProjectChat } from '../../lib/api';
import { asleep, chatKey, fetchThread, isSilent, loadOlder, toolsReload } from '../../lib/chat';
import { useT, type MessageKey } from '../../lib/i18n';
import { useProjectName } from '../../lib/useProjectName';
import { cn, errorMessage } from '../../lib/utils';
import { useReadAloud } from '../../lib/voice/useReadAloud';
import { ConfirmDialog } from '../ConfirmDialog';
import { ProjectCredentialCards } from '../CredentialCard';
import { AIIcon, aiLabel } from '../state';
import { Button } from '../ui/button';
import { Notice } from '../ui/card';
import { Tip } from '../ui/tooltip';
import { PageStack } from '../pages/PageStack';
import { Composer } from './Composer';
import { FindBar } from './FindBar';
import { useRevealItem, type ChatOpenAt } from './reveal';
import { ReadAloudControls } from './ReadAloud';
import { Timeline } from './Timeline';

// ChatTab is the conversation with an agent's AI tool: the timeline, with the
// composer floating over its end.
// autoStart is off only where nothing should start, like the dev preview.
// openAt brings an item into view, reading the chat back to it if need be,
// with the find bar open on openAt.query when there is one; a new object
// does it again, so pass one per opening.
export function ChatTab({
  agent,
  starting,
  onStart,
  autoStart = true,
  onOpenAgent,
  openAt,
}: {
  agent: T.Agent;
  starting: boolean;
  onStart: () => void;
  autoStart?: boolean;
  onOpenAgent?: (ref: string) => void;
  openAt?: ChatOpenAt;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const thread = useQuery({ queryKey: chatKey(agent.ref), queryFn: () => fetchThread(queryClient, agent.ref) });
  const session = thread.data?.session;
  const running = agent.state === 'running';
  // A stopped or paused agent's chat is still all there to read, with the
  // button that starts it on the composer, which also wakes it by sending.
  const sleeping = asleep(agent);
  useReadAloud(agent.ref, thread.data?.items);
  const start = useMutation({
    mutationFn: () => api.startChat(agent.ref),
    // Starting a project's chat for the first time makes its lead, with a
    // worktree of its own: read the project's chat again to have it.
    onSuccess: () => {
      if (isProjectChat(agent.ref)) void queryClient.invalidateQueries({ queryKey: ['projectChat', agent.project] });
    },
  });

  // Start the AI tool when the chat opens, so its settings — model, effort,
  // mode — are there before you type. They are the tool's own menus, and only
  // a running session has them. A project's chat starts here too, on its
  // first opening: a project that's never opened still costs nothing.
  const sessionState = session?.state;
  useEffect(() => {
    if (autoStart && running && sessionState === 'off' && !start.isPending) start.mutate();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoStart, running, sessionState, agent.ref]);

  // Follow the conversation while you're at its end.
  const scroller = useRef<HTMLDivElement>(null);
  const content = useRef<HTMLDivElement>(null);
  const composer = useRef<HTMLDivElement>(null);
  const following = useRef(true);
  const lastTop = useRef(0);
  const [atEnd, setAtEnd] = useState(true);
  const [composerHeight, setComposerHeight] = useState(120);

  const scrollToEnd = () => {
    following.current = true;
    setAtEnd(true);
    const box = scroller.current;
    if (box) box.scrollTo({ top: box.scrollHeight, behavior: 'smooth' });
  };

  useLayoutEffect(() => {
    const box = scroller.current;
    const inner = content.current;
    const bottom = composer.current;
    if (!box || !inner || !bottom) return;
    const follow = () => {
      if (following.current) box.scrollTop = box.scrollHeight;
    };
    const observer = new ResizeObserver(() => {
      setComposerHeight(bottom.offsetHeight);
      follow();
    });
    // The room left for the composer is padding, which only the border box includes.
    observer.observe(inner, { box: 'border-box' });
    observer.observe(bottom);
    follow();
    return () => observer.disconnect();
  }, []);

  // A banner on the composer, like a permission request, takes room at the end: keep the end in view.
  useLayoutEffect(() => {
    const box = scroller.current;
    if (box && following.current) box.scrollTop = box.scrollHeight;
  }, [composerHeight]);

  // Older messages come a page at a time as you get near the top. A page put
  // in front would push what you are reading down by its height, so the
  // distance from the end is kept across it instead: what was on screen stays
  // where it was. (The scroller has overflow-anchor off, for following the end.)
  const [loadingOlder, setLoadingOlder] = useState(false);
  const fromEnd = useRef<number | null>(null);
  const older = thread.data?.older ?? false;
  const loadMore = () => {
    const box = scroller.current;
    if (!box || !older || loadingOlder) return;
    setLoadingOlder(true);
    fromEnd.current = box.scrollHeight - box.scrollTop;
    loadOlder(queryClient, agent.ref)
      .catch(() => {})
      .finally(() => setLoadingOlder(false));
  };
  const firstId = thread.data?.items[0]?.id;
  useLayoutEffect(() => {
    const box = scroller.current;
    if (!box || fromEnd.current === null) return;
    if (!following.current) box.scrollTop = box.scrollHeight - fromEnd.current;
    fromEnd.current = null;
  }, [firstId]);
  // A first page too short to scroll has no top to get near: keep reading
  // back until the chat fills its view, or there is nothing older.
  useEffect(() => {
    const box = scroller.current;
    if (box && older && !loadingOlder && box.scrollHeight <= box.clientHeight + 200) loadMore();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [older, loadingOlder, firstId]);

  // Ctrl+F (⌘F) finds in the chat on screen. Every chat open in the app
  // listens, and only one is visible.
  const root = useRef<HTMLDivElement>(null);
  const [finding, setFinding] = useState(false);
  const [findFocus, setFindFocus] = useState(0);
  const [findStart, setFindStart] = useState<ChatOpenAt>();
  const [openings, setOpenings] = useState(0);
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key.toLowerCase() !== 'f' || !(event.ctrlKey || event.metaKey) || event.altKey || event.shiftKey) return;
      if (!root.current?.checkVisibility()) return;
      event.preventDefault();
      setFinding(true);
      setFindFocus((n) => n + 1);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const jumped = () => {
    following.current = false;
    setAtEnd(false);
  };
  const { reveal } = useRevealItem(agent.ref, thread.data, scroller, jumped);
  useEffect(() => {
    if (!openAt) return;
    if (openAt.query) {
      // A fresh bar, on the query, starting at the item.
      setFindStart(openAt);
      setOpenings((n) => n + 1);
      setFinding(true);
      setFindFocus((n) => n + 1);
    } else reveal(openAt.item);
  }, [openAt, reveal]);

  // A conversation of nothing but notices — a project's chat whose only agent
  // finished without waking it — has items and still nothing to show, so the
  // hero belongs there too.
  const empty = !thread.data?.items.some((it) => !isSilent(it));
  return (
    <div ref={root} className="relative flex h-full min-h-0 flex-col bg-chat" data-chat={agent.ref}>
      {finding && (
        <FindBar
          key={openings}
          chatRef={agent.ref}
          thread={thread.data}
          scroller={scroller}
          focus={findFocus}
          start={findStart}
          onJump={jumped}
          onClose={() => {
            setFinding(false);
            setFindStart(undefined);
          }}
        />
      )}
      <div
        ref={scroller}
        className="min-h-0 flex-1 overflow-y-auto overscroll-contain [overflow-anchor:none]"
        onScroll={(event) => {
          const box = event.currentTarget;
          const end = box.scrollHeight - box.scrollTop - box.clientHeight < 48;
          // Scrolling up stops following, and getting back to the end follows again.
          // A smooth scroll down to the end passes the middle, which doesn't count.
          if (end) following.current = true;
          else if (box.scrollTop < lastTop.current - 1) following.current = false;
          lastTop.current = box.scrollTop;
          setAtEnd(end || following.current);
          if (box.scrollTop < 400) loadMore();
        }}
      >
        <div ref={content} className="mx-auto w-full max-w-4xl px-4 pt-6 md:px-6" style={{ paddingBottom: composerHeight + 28 }}>
          {thread.isPending ? (
            <div className="grid gap-3 pt-2" aria-label={t('chat.tab.loading')}>
              <div className="skeleton ml-auto h-10 w-2/5 rounded-2xl" />
              <div className="skeleton h-4 w-3/4 rounded-md" />
              <div className="skeleton h-4 w-2/3 rounded-md" />
            </div>
          ) : thread.error ? (
            <Notice>{errorMessage(thread.error)}</Notice>
          ) : empty ? (
            <Hero agent={agent} />
          ) : (
            <>
              {thread.data.older && (
                <div className="flex h-8 items-center justify-center gap-1.5 pb-4 text-[12px] text-subtle" data-chat-older>
                  {loadingOlder && <LoaderCircle className="size-3.5 animate-spin" />}
                  {loadingOlder ? t('chat.tab.loadingOlder') : ''}
                </div>
              )}
              <Timeline agent={agent} thread={thread.data} onOpenAgent={onOpenAgent} />
            </>
          )}
          {/* What the project's agents are waiting on you for, at the end of
              the project's chat where you are: cards, not messages the lead reads. */}
          {isProjectChat(agent.ref) && !isHomeChat(agent.ref) && !thread.isPending && <ProjectCredentialCards project={agent.ref.split('/')[0]} />}
        </div>
      </div>

      {!atEnd && (
        <button
          className="absolute left-1/2 z-20 flex -translate-x-1/2 animate-fade-in items-center gap-1.5 rounded-full border border-line-strong bg-overlay px-3 py-1.5 text-[12px] text-tertiary shadow-lg backdrop-blur-xl transition hover:text-title"
          style={{ bottom: composerHeight + 18 }}
          onClick={scrollToEnd}
        >
          <ArrowDown className="size-3.5" />
          {t('chat.tab.scrollToEnd')}
        </button>
      )}

      <div ref={composer} className="pointer-events-none absolute inset-x-0 bottom-0 px-3 pb-3 md:px-6 md:pb-5">
        <div className="pointer-events-auto relative mx-auto w-full max-w-4xl">
          <PageStack agent={agent} />
          <Composer
            agent={agent}
            thread={thread.data}
            disabled={!running && !sleeping}
            asleep={sleeping ? { state: sleeping, starting, onStart } : undefined}
            onSent={scrollToEnd}
          />
        </div>
      </div>

    </div>
  );
}

// ChatHeaderControls is the chat's live status and its "New chat" action: the
// bit of ChatTab that used to be its own header row. The callers (AgentView,
// ProjectView) fold it into their tab bar instead, so the chat itself doesn't
// need a second header under theirs.
export function ChatHeaderControls({ agent }: { agent: T.Agent }) {
  const t = useT();
  const queryClient = useQueryClient();
  const projectName = useProjectName(agent.project);
  const thread = useQuery({ queryKey: chatKey(agent.ref), queryFn: () => fetchThread(queryClient, agent.ref) });
  const items = thread.data?.items ?? [];
  const [clearing, setClearing] = useState(false);
  return (
    <>
      <SessionStatus agent={agent} session={thread.data?.session} />
      <ReadAloudControls />
      <ReloadTools agent={agent} session={thread.data?.session} />
      <Tip label={t('chat.tab.newChatTip')}>
        <Button size="sm" variant="ghost" className="h-7 px-2 sm:px-2.5" disabled={items.length === 0} onClick={() => setClearing(true)}>
          <MessageSquarePlus />
          <span className="hidden sm:inline">{t('chat.tab.newChat')}</span>
        </Button>
      </Tip>
      <ConfirmDialog
        open={clearing}
        onOpenChange={setClearing}
        title={t('chat.tab.newChatTitle')}
        description={t('chat.tab.newChatDescription', {
          scope: isHomeChat(agent.ref) ? 'home' : isProjectChat(agent.ref) ? 'project' : 'agent',
          tool: aiLabel(agent.ai),
          project: projectName,
          name: agent.title || agent.name,
        })}
        confirmLabel={t('chat.tab.newChat')}
        onConfirm={() => api.clearChat(agent.ref)}
      />
    </>
  );
}

// ReloadTools restarts the chat's AI tool with the connectors and MCP servers
// it has now, which it reads only as it starts, resuming the same session so
// the conversation carries on. An adapter that can't resume asks first.
function ReloadTools({ agent, session }: { agent: T.Agent; session?: T.ChatSession }) {
  const t = useT();
  const [confirming, setConfirming] = useState(false);
  const reload = useMutation({ mutationFn: () => api.reloadChatTools(agent.ref), onError: (err) => toast.error(errorMessage(err)) });
  const tool = aiLabel(agent.ai);
  const can = toolsReload(session);
  const tip =
    can === 'busy'
      ? t('chat.tab.reloadToolsBusy', { tool })
      : can === 'off'
        ? t('chat.tab.reloadToolsOff', { tool })
        : t(session?.noResume ? 'chat.tab.reloadToolsNoResumeTip' : 'chat.tab.reloadToolsTip', { tool });
  return (
    <>
      <Tip label={tip}>
        {/* A span, so the tip still shows on the disabled button. */}
        <span>
          <Button
            size="sm"
            variant="ghost"
            className="h-7 px-2 sm:px-2.5"
            disabled={can !== 'ready' || reload.isPending}
            onClick={() => (session?.noResume ? setConfirming(true) : reload.mutate())}
            aria-label={t('chat.tab.reloadTools')}
            data-chat-reload-tools
          >
            <RefreshCw className={cn(reload.isPending && 'animate-spin')} />
            <span className="hidden 2xl:inline">{t('chat.tab.reloadTools')}</span>
          </Button>
        </span>
      </Tip>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('chat.tab.reloadToolsTitle')}
        description={t('chat.tab.reloadToolsDescription', { tool })}
        confirmLabel={t('chat.tab.reloadToolsConfirm')}
        onConfirm={() => api.reloadChatTools(agent.ref)}
      />
    </>
  );
}

function Hero({ agent }: { agent: T.Agent }) {
  const t = useT();
  const projectName = useProjectName(agent.project);
  if (!isProjectChat(agent.ref)) return <AgentHero agent={agent} />;
  if (isHomeChat(agent.ref)) return <HomeHero agent={agent} />;
  return (
    <div className="flex min-h-[42vh] animate-slide-up flex-col items-center justify-center pt-6 text-center" data-chat-hero>
      <div className="relative mb-5">
        <div className="brand-gradient absolute inset-0 rounded-2xl opacity-25 blur-xl" />
        <div className="relative flex size-12 items-center justify-center rounded-2xl border border-line-strong bg-overlay">
          <AIIcon ai={agent.ai} className="size-5 text-brand-300" />
        </div>
      </div>
      <h2 className="text-balance text-2xl font-normal tracking-tight text-primary">{t('chat.hero.title', { project: projectName })}</h2>
      <p className="mt-2 max-w-md text-balance text-sm leading-relaxed text-subtle">
        {t.rich('chat.hero.project', {
          project: projectName,
          branch: <span className="font-mono text-[12.5px] text-muted">{agent.baseRef || t('chat.hero.itsBranch')}</span>,
        })}
      </p>
    </div>
  );
}

function HomeHero({ agent }: { agent: T.Agent }) {
  const t = useT();
  return (
    <div className="flex min-h-[42vh] animate-slide-up flex-col items-center justify-center pt-6 text-center" data-chat-hero>
      <div className="relative mb-5">
        <div className="brand-gradient absolute inset-0 rounded-2xl opacity-25 blur-xl" />
        <div className="relative flex size-12 items-center justify-center rounded-2xl border border-line-strong bg-overlay">
          <AIIcon ai={agent.ai} className="size-5 text-brand-300" />
        </div>
      </div>
      <h2 className="text-balance text-2xl font-normal tracking-tight text-primary">{t('chat.hero.homeTitle')}</h2>
      <p className="mt-2 max-w-md text-balance text-sm leading-relaxed text-subtle">
        {t('chat.hero.home')}
      </p>
    </div>
  );
}

function AgentHero({ agent }: { agent: T.Agent }) {
  const t = useT();
  const projectName = useProjectName(agent.project);
  return (
    <div className="flex min-h-[42vh] animate-slide-up flex-col items-center justify-center pt-6 text-center" data-chat-hero>
      <div className="relative mb-5">
        <div className="brand-gradient absolute inset-0 rounded-2xl opacity-25 blur-xl" />
        <div className="relative flex size-12 items-center justify-center rounded-2xl border border-line-strong bg-overlay">
          <AIIcon ai={agent.ai} className="size-5 text-brand-300" />
        </div>
      </div>
      <h2 className="text-balance text-2xl font-normal tracking-tight text-primary">{t('chat.hero.title', { project: projectName })}</h2>
      <p className="mt-2 max-w-md text-balance text-sm leading-relaxed text-subtle">
        {t.rich('chat.hero.agent', { tool: aiLabel(agent.ai), branch: <span className="font-mono text-[12.5px] text-muted">{agent.branch}</span> })}
      </p>
    </div>
  );
}

const statusStyles: Record<string, { dot: string; label: MessageKey }> = {
  off: { dot: 'bg-faint', label: 'chat.status.off' },
  starting: { dot: 'bg-muted', label: 'chat.status.starting' },
  ready: { dot: 'bg-emerald-400', label: 'chat.status.ready' },
  running: { dot: 'bg-sky-400 animate-pulse', label: 'chat.status.running' },
  waiting: { dot: 'bg-amber-400 animate-pulse', label: 'chat.status.waiting' },
  // Ready, with work it left running in the background: not the session's own
  // state, but what a ready session with ChatSession.background is.
  awaiting: { dot: 'bg-violet-400 animate-pulse', label: 'chat.status.awaiting' },
  error: { dot: 'bg-rose-400', label: 'chat.status.error' },
};

function SessionStatus({ agent, session }: { agent: T.Agent; session?: T.ChatSession }) {
  const t = useT();
  const background = session?.background ?? [];
  const state = session?.state === 'ready' && background.length > 0 ? 'awaiting' : (session?.state ?? 'off');
  const style = statusStyles[state] ?? statusStyles.off;
  const tool = aiLabel(agent.ai);
  const tip = state === 'awaiting' ? t('chat.status.awaitingTip', { tasks: background.join(', ') }) : session?.error || session?.detail || session?.adapter || t('chat.status.adapter', { tool });
  return (
    <>
      <Tip label={tip}>
        <span className="flex min-w-0 items-center gap-2 text-[12.5px] text-muted" data-chat-state={state}>
          {state === 'starting' ? <LoaderCircle className="size-3 animate-spin text-muted" /> : <span className={cn('size-1.5 shrink-0 rounded-full', style.dot)} />}
          <span className="hidden truncate sm:inline">{t(style.label, { tool })}</span>
        </span>
      </Tip>
      {/* Only Claude Code's adapter reports background tasks: elsewhere an
          agent waiting on one reads as finished, and never wakes for it. */}
      {(agent.ai === 'codex' || agent.ai === 'opencode' || agent.ai === 'cursor') && (
        <Tip label={t('chat.background.untrackedTip', { tool })}>
          <span className="flex min-w-0 items-center gap-1.5 text-[12px] text-amber-300/80" data-chat-background-untracked>
            <EyeOff className="size-3.5 shrink-0" />
            <span className="hidden truncate lg:inline">{t('chat.background.untracked')}</span>
          </span>
        </Tip>
      )}
    </>
  );
}
