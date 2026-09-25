import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowDown, LoaderCircle, MessageSquarePlus, Play } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type * as T from '../../../shared/api';
import { api, isProjectChat } from '../../lib/api';
import { chatKey, fetchThread, isSilent } from '../../lib/chat';
import { cn, errorMessage } from '../../lib/utils';
import { ConfirmDialog } from '../ConfirmDialog';
import { ProjectCredentialCards } from '../CredentialCard';
import { AIIcon, aiLabel } from '../state';
import { Button } from '../ui/button';
import { Notice } from '../ui/card';
import { Tip } from '../ui/tooltip';
import { Composer } from './Composer';
import { Timeline } from './Timeline';

// ChatTab is the conversation with an agent's AI tool: the timeline, with the
// composer floating over its end.
// autoStart is off only where nothing should start, like the dev preview.
export function ChatTab({ agent, starting, onStart, autoStart = true }: { agent: T.Agent; starting: boolean; onStart: () => void; autoStart?: boolean }) {
  const queryClient = useQueryClient();
  const thread = useQuery({ queryKey: chatKey(agent.ref), queryFn: () => fetchThread(agent.ref) });
  const session = thread.data?.session;
  const running = agent.state === 'running';
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

  // A conversation of nothing but notices — a project's chat whose only agent
  // finished without waking it — has items and still nothing to show, so the
  // hero belongs there too.
  const empty = !thread.data?.items.some((it) => !isSilent(it));
  return (
    <div className="relative flex h-full min-h-0 flex-col bg-chat" data-chat={agent.ref}>
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
        }}
      >
        <div ref={content} className="mx-auto w-full max-w-4xl px-4 pt-6 md:px-6" style={{ paddingBottom: composerHeight + 28 }}>
          {thread.isPending ? (
            <div className="grid gap-3 pt-2" aria-label="Loading the conversation">
              <div className="skeleton ml-auto h-10 w-2/5 rounded-2xl" />
              <div className="skeleton h-4 w-3/4 rounded-md" />
              <div className="skeleton h-4 w-2/3 rounded-md" />
            </div>
          ) : thread.error ? (
            <Notice>{errorMessage(thread.error)}</Notice>
          ) : empty ? (
            <Hero agent={agent} />
          ) : (
            <Timeline agent={agent} thread={thread.data} />
          )}
          {/* What the project's agents are waiting on you for, at the end of
              the project's chat where you are: cards, not messages the lead reads. */}
          {isProjectChat(agent.ref) && !thread.isPending && <ProjectCredentialCards project={agent.ref.split('/')[0]} />}
        </div>
      </div>

      {!atEnd && (
        <button
          className="absolute left-1/2 z-20 flex -translate-x-1/2 animate-fade-in items-center gap-1.5 rounded-full border border-line-strong bg-overlay px-3 py-1.5 text-[12px] text-tertiary shadow-lg backdrop-blur-xl transition hover:text-title"
          style={{ bottom: composerHeight + 18 }}
          onClick={scrollToEnd}
        >
          <ArrowDown className="size-3.5" />
          Scroll to end
        </button>
      )}

      <div ref={composer} className="pointer-events-none absolute inset-x-0 bottom-0 px-3 pb-3 md:px-6 md:pb-5">
        <div className="pointer-events-auto mx-auto w-full max-w-4xl">
          <Composer agent={agent} thread={thread.data} disabled={!running} onSent={scrollToEnd} />
        </div>
      </div>

      {!isProjectChat(agent.ref) && (agent.state === 'stopped' || agent.state === 'paused') && (
        <div className="absolute inset-0 z-30 flex animate-fade-in items-center justify-center bg-chat/75 backdrop-blur-[2px]">
          <div className="panel grid max-w-sm justify-items-center gap-3 rounded-2xl px-8 py-7 text-center">
            <p className="text-sm text-tertiary">
              {agent.title || agent.name} is {agent.state}.{' '}
              {agent.state === 'paused' ? 'Its processes are frozen until you resume it.' : `Start it to chat with ${aiLabel(agent.ai)}.`}
            </p>
            <Button variant="primary" onClick={onStart} disabled={starting}>
              {starting ? <LoaderCircle className="animate-spin" /> : <Play />}
              {agent.state === 'paused' ? 'Resume' : 'Start'} {agent.name}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

// ChatHeaderControls is the chat's live status and its "New chat" action: the
// bit of ChatTab that used to be its own header row. The callers (AgentView,
// ProjectView) fold it into their tab bar instead, so the chat itself doesn't
// need a second header under theirs.
export function ChatHeaderControls({ agent }: { agent: T.Agent }) {
  const thread = useQuery({ queryKey: chatKey(agent.ref), queryFn: () => fetchThread(agent.ref) });
  const items = thread.data?.items ?? [];
  const [clearing, setClearing] = useState(false);
  return (
    <>
      <SessionStatus agent={agent} session={thread.data?.session} />
      <Tip label="Start a new conversation">
        <Button size="sm" variant="ghost" className="h-7 px-2 sm:px-2.5" disabled={items.length === 0} onClick={() => setClearing(true)}>
          <MessageSquarePlus />
          <span className="hidden sm:inline">New chat</span>
        </Button>
      </Tip>
      <ConfirmDialog
        open={clearing}
        onOpenChange={setClearing}
        title="Start a new chat?"
        description={
          isProjectChat(agent.ref)
            ? `${aiLabel(agent.ai)} starts a new session that doesn't remember this conversation, which is removed. The agents of ${agent.project} and their work are untouched.`
            : `${aiLabel(agent.ai)} starts a new session that doesn't remember this conversation, which is removed. What ${agent.title || agent.name} changed in its worktree stays.`
        }
        confirmLabel="New chat"
        onConfirm={() => api.clearChat(agent.ref)}
      />
    </>
  );
}

function Hero({ agent }: { agent: T.Agent }) {
  if (!isProjectChat(agent.ref)) return <AgentHero agent={agent} />;
  return (
    <div className="flex min-h-[42vh] animate-slide-up flex-col items-center justify-center pt-6 text-center" data-chat-hero>
      <div className="relative mb-5">
        <div className="brand-gradient absolute inset-0 rounded-2xl opacity-25 blur-xl" />
        <div className="relative flex size-12 items-center justify-center rounded-2xl border border-line-strong bg-overlay">
          <AIIcon ai={agent.ai} className="size-5 text-brand-300" />
        </div>
      </div>
      <h2 className="text-balance text-2xl font-normal tracking-tight text-primary">What should we build in {agent.project}?</h2>
      <p className="mt-2 max-w-md text-balance text-sm leading-relaxed text-subtle">
        This chat reads {agent.project} on <span className="font-mono text-[12.5px] text-muted">{agent.baseRef || 'its branch'}</span>. It doesn't run or
        change anything itself: the work happens in agents, each on its own machine.
      </p>
    </div>
  );
}

function AgentHero({ agent }: { agent: T.Agent }) {
  return (
    <div className="flex min-h-[42vh] animate-slide-up flex-col items-center justify-center pt-6 text-center" data-chat-hero>
      <div className="relative mb-5">
        <div className="brand-gradient absolute inset-0 rounded-2xl opacity-25 blur-xl" />
        <div className="relative flex size-12 items-center justify-center rounded-2xl border border-line-strong bg-overlay">
          <AIIcon ai={agent.ai} className="size-5 text-brand-300" />
        </div>
      </div>
      <h2 className="text-balance text-2xl font-normal tracking-tight text-primary">What should we build in {agent.project}?</h2>
      <p className="mt-2 max-w-md text-balance text-sm leading-relaxed text-subtle">
        {aiLabel(agent.ai)} works on its own machine, on the branch <span className="font-mono text-[12.5px] text-muted">{agent.branch}</span>.
      </p>
    </div>
  );
}

const statusStyles: Record<string, { dot: string; label: (tool: string) => string }> = {
  off: { dot: 'bg-faint', label: () => 'Not started' },
  starting: { dot: 'bg-muted', label: (tool) => `Starting ${tool}` },
  ready: { dot: 'bg-emerald-400', label: (tool) => `${tool} is ready` },
  running: { dot: 'bg-sky-400 animate-pulse', label: () => 'Working' },
  waiting: { dot: 'bg-amber-400 animate-pulse', label: () => 'Waiting for you' },
  error: { dot: 'bg-rose-400', label: (tool) => `${tool} stopped` },
};

function SessionStatus({ agent, session }: { agent: T.Agent; session?: T.ChatSession }) {
  const state = session?.state ?? 'off';
  const style = statusStyles[state] ?? statusStyles.off;
  const tool = aiLabel(agent.ai);
  return (
    <Tip label={session?.error || session?.detail || session?.adapter || `${tool} through its ACP adapter`}>
      <span className="flex min-w-0 items-center gap-2 text-[12.5px] text-muted" data-chat-state={state}>
        {state === 'starting' ? <LoaderCircle className="size-3 animate-spin text-muted" /> : <span className={cn('size-1.5 shrink-0 rounded-full', style.dot)} />}
        <span className="hidden truncate sm:inline">{style.label(tool)}</span>
      </span>
    </Tip>
  );
}
