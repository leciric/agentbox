import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { GitFork, History, MessagesSquare, Plus } from 'lucide-react';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import * as T from '../../shared/api';
import { api, sessionOfRef } from '../lib/api';
import { chatReadyRetry, forgetThread, loadErrorMessage } from '../lib/chat';
import { t, useT } from '../lib/i18n';
import { inSession, pendingReveal, requestReveal, useChatOpenAt, useReveal, type Reveal } from '../lib/reveal';
import { afterClose, awake, chooseSession, markSessionSeen, moveTab, neighbour, pickSession, sessionColor, showingSession, snoozedOf, sortTabs, useChosenSession, withSession } from '../lib/sessions';
import { requestSessionFocus } from '../lib/newTask';
import { keysHint, sessionJump, sessionKeys, sessionShortcut } from '../lib/shortcuts';
import { cn, errorMessage } from '../lib/utils';
import { ChatTab } from './chat/ChatTab';
import { SessionHeader } from './chat/SessionHeader';
import { SessionHistory, useClosedSessions } from './chat/SessionHistory';
import { SessionTabs } from './chat/SessionTabs';
import { Button } from './ui/button';
import { EmptyState } from './ui/card';
import { ReviewCount } from './v2/ReviewV2';

// leadAgentFrom builds the Agent ChatTab expects out of a project's chat: the
// lead runs on the host, with no machine of its own, so it's not a real entry
// in the agents list. Each session of the chat has a lead of its own.
export function leadAgentFrom(project: T.Project, info: T.ProjectChat, topic?: string): T.Agent {
  return {
    ref: info.ref,
    project: project.name,
    name: info.ref.split('/')[1] || T.LeadName,
    title: topic || t('chat.project.agentTitle'),
    instance: '',
    ai: 'claude',
    autonomous: false,
    branch: '', // it stands on baseRef, detached, and commits nothing
    baseRef: info.baseRef || project.branch,
    baseCommit: '',
    worktree: info.worktree || '',
    source: '',
    claudeAccount: project.claudeAccount,
    githubAccount: '', // the lead has no shell, so no use for a GitHub token
    interface: 'chat',
    chat: info.chat,
    // There is no machine to start: "running" is how ChatTab asks whether the
    // conversation can run at all, and a project's chat always can.
    state: 'running',
    ip: '',
    createdAt: project.createdAt,
  };
}

// useLeadSessions is a project's chat sessions, or the Home chat's
// (T.HomeProject), kept current by their events (lib/events.ts).
export function useLeadSessions(project: string) {
  return useQuery({ queryKey: ['leadSessions', project], queryFn: () => api.leadSessions(project) });
}

// useCurrentSession is the session a project's chat, or the Home chat, shows,
// the one its tabs have chosen; undefined while they load, or with none.
export function useCurrentSession(project: string | undefined): T.LeadSession | undefined {
  const sessions = useQuery({ queryKey: ['leadSessions', project], queryFn: () => api.leadSessions(project!), enabled: !!project });
  const chosen = useChosenSession(project ?? '');
  return project ? pickSession(awake(sessions.data ?? []), chosen) : undefined;
}

// showSession shows a session of a chat, opened or moved to, to write in: its
// composer takes the focus once it shows.
function showSession(project: string, s: T.LeadSession) {
  chooseSession(project, s.id);
  requestSessionFocus(s.ref);
}

// The project's chat, driven by its leads: one per session, the one shown
// picked in the tabs above it (SessionChatPanel). ChatTab is shared with the
// agents' chats, so the lead is handed to it as an agent.
//
// The conversation is what you and the lead said to each other, and only that.
// What the project's agents are doing is in the right-hand panel's agent list
// beside it (v2/AgentList), rather than notices in the middle of it.
//
// Opening it starts the lead's AI tool, with no turn, so its settings can be
// chosen before the first message. A session whose chat is never opened costs
// nothing; a project with no session shows an empty state that opens one.
//
// A new session asks for nothing: it opens untitled, on the tool, model, mode
// and effort of the session shown, with its composer focused, and its first
// turn names it (internal/daemon/sessionname.go).
export function ProjectChatPanel({ project }: { project: T.Project }) {
  const openAt = useChatOpenAt(project.name);
  return (
    <SessionChatPanel project={project.name} card>
      {(info, current) => (
        // Above the composer: how many of its agents wait for review.
        <ChatTab
          key={info.ref}
          agent={leadAgentFrom(project, info, current.topic)}
          starting={false}
          onStart={() => {}}
          openAt={inSession(openAt, current.id)}
          aboveComposer={<ReviewCount project={project.name} variant="banner" />}
        />
      )}
    </SessionChatPanel>
  );
}

// SessionChatPanel is a chat in sessions, a project's or the Home chat's
// (project T.HomeProject): the tabs above it, their keys, the empty state of a
// chat with none open, and its history of closed sessions (SessionHistory),
// in the chat's place while it is open. children draws the session shown,
// once its chat has answered. card draws the box the chat is in (a project's
// has no other). It takes no session colour: that is on the tabs.
export function SessionChatPanel({ project, card, children }: { project: string; card?: boolean; children: (info: T.ProjectChat, current: T.LeadSession) => ReactNode }) {
  const t = useT();
  const queryClient = useQueryClient();
  // A project just added can be asked about before the daemon has it: wait
  // for it rather than say the chat is unavailable (chatReadyRetry).
  const sessions = useQuery({ queryKey: ['leadSessions', project], queryFn: () => api.leadSessions(project), ...chatReadyRetry });
  const chosen = useChosenSession(project);
  // The tabs are the sessions not snoozed; the snoozed wait at the row's end.
  const all = sessions.data ?? [];
  const list = awake(all);
  const sleeping = snoozedOf(all);
  const current = pickSession(list, chosen);
  const color = sessionColor(list, current);
  const box = (content: ReactNode) => (
    <div className={cn('relative flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border', card ? 'border-line' : 'border-transparent')}>
      {content}
    </div>
  );
  const closed = useClosedSessions(project);
  const [history, setHistory] = useState(false);
  // The closed session the history opens on: one a search found.
  const [historyAt, setHistoryAt] = useState<string>();
  const openHistory = (at?: string) => {
    setHistoryAt(at);
    setHistory(true);
  };
  const chat = useQuery({
    queryKey: ['projectChat', project, current?.id],
    queryFn: () => api.projectChat(project, current!.id),
    enabled: !!current,
    ...chatReadyRetry,
  });
  const info = chat.data;

  const refresh = () => void queryClient.invalidateQueries({ queryKey: ['leadSessions', project] });
  // opened shows a session just made, on what its creation answered, rather
  // than after the sessions and then its chat are read again: until then the
  // tabs showed another session, and the composer wasn't there. Its AI tool
  // starts now, not once the chat has mounted and read its thread, and what
  // you send before it is ready waits for it (chat.Manager). Its id is new,
  // never a closed or deleted session's, so no thread is held for it.
  const opened = (s: T.LeadSession) => {
    setHistory(false);
    queryClient.setQueryData<T.LeadSession[]>(['leadSessions', project], (list) => withSession(list ?? [], s));
    queryClient.setQueryData<T.ProjectChat>(['projectChat', project, s.id], (had) => had ?? { project: project, session: s.id, ref: s.ref, started: false, chat: s.chat });
    showSession(project, s);
    void api.startChat(s.ref).catch(() => {}); // ChatTab starts it again, and says why it can't
    refresh();
  };
  const add = useMutation({
    mutationFn: (from: string | undefined) => api.newLeadSession(project, { from }),
    onSuccess: opened,
    onError: (err) => toast.error(errorMessage(err)),
  });
  const reopen = useMutation({
    mutationFn: (id: string) => api.reopenLeadSession(project, id),
    onSuccess: (s) => {
      refresh();
      showSession(project, s);
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  // Closing keeps the session, in the history: the tab goes at once, and the
  // toast offers it back.
  const close = useMutation({
    mutationFn: (id: string) => api.closeLeadSession(project, id),
    onMutate: (id) => {
      const next = afterClose(list, id);
      if (next && id === current?.id) chooseSession(project, next.id);
      queryClient.setQueryData<T.LeadSession[]>(['leadSessions', project], (had) => had?.filter((s) => s.id !== id));
    },
    onSuccess: (s) => {
      // Its chat stopped: read again from the store when it is next shown.
      forgetThread(queryClient, s.ref);
      refresh();
      toast(t('sessions.closed', { topic: s.topic }), { action: { label: t('sessions.reopen'), onClick: () => reopen.mutate(s.id) } });
    },
    onError: (err) => {
      refresh();
      toast.error(errorMessage(err));
    },
  });
  // Snoozing hides the tab at once, as closing does; waking shows it.
  const snooze = useMutation({
    mutationFn: ({ id, until }: { id: string; until?: Date }) => api.snoozeLeadSession(project, id, until),
    onMutate: ({ id, until }) => {
      if (!until) return;
      const next = afterClose(list, id);
      if (next && id === current?.id) chooseSession(project, next.id);
      queryClient.setQueryData<T.LeadSession[]>(['leadSessions', project], (had) => had?.map((s) => (s.id === id ? { ...s, snoozedUntil: until.toISOString() } : s)));
    },
    onSuccess: (s, { until }) => {
      refresh();
      if (until) toast(t('sessions.snoozed.done', { topic: s.topic }), { action: { label: t('sessions.snoozed.wake'), onClick: () => snooze.mutate({ id: s.id }) } });
      else showSession(project, s);
    },
    onError: (err) => {
      refresh();
      toast.error(errorMessage(err));
    },
  });
  const rename = useMutation({
    mutationFn: ({ id, topic }: { id: string; topic: string }) => api.renameLeadSession(project, id, topic),
    onSuccess: refresh,
    onError: (err) => toast.error(errorMessage(err)),
  });
  // Pins and the tabs' order are kept by the daemon, for every computer and
  // the phone; the tabs move at once and the answer settles them.
  const reorder = (next: T.LeadSession[]) => {
    queryClient.setQueryData<T.LeadSession[]>(['leadSessions', project], next);
    api
      .orderLeadSessions(
        project,
        next.map((s) => s.id),
      )
      .then((list) => queryClient.setQueryData(['leadSessions', project], list))
      .catch((err) => {
        refresh();
        toast.error(errorMessage(err));
      });
  };
  const pin = useMutation({
    mutationFn: ({ id, pinned }: { id: string; pinned: boolean }) => api.pinLeadSession(project, id, pinned),
    onMutate: ({ id, pinned }) => {
      queryClient.setQueryData<T.LeadSession[]>(['leadSessions', project], (had) => had && sortTabs(had.map((s) => (s.id === id ? { ...s, pinned } : s))));
    },
    onSettled: refresh,
    onError: (err) => toast.error(errorMessage(err)),
  });

  // A session a search found (lib/reveal.ts, lib/search.ts): shown among the
  // tabs while it is open, or read in the history once closed. Each request
  // is followed once, so the tabs read again don't pull you back to it.
  const reveal = useReveal();
  const followed = useRef<Reveal | null>(null);
  useEffect(() => {
    const target = reveal?.session;
    if (!target || target.project !== project || !sessions.data || followed.current === reveal || pendingReveal() !== reveal) return;
    followed.current = reveal;
    const open = sessions.data.find((s) => s.id === target.id);
    if (open) {
      setHistory(false);
      chooseSession(project, open.id);
    } else {
      openHistory(target.id);
    }
  }, [reveal, project, sessions.data]);

  // What is on screen is seen: now, and whenever it says more.
  const shownId = current?.id;
  const shownLast = current?.last?.at;
  useEffect(() => {
    if (shownId) markSessionSeen(project, shownId);
  }, [project, shownId, shownLast]);
  useEffect(() => (shownId ? showingSession(project, shownId) : undefined), [project, shownId]);

  // The sessions' keys (lib/shortcuts.ts), while this chat is on screen.
  const keys = useRef({ list, current, history, open: add.mutate, close: close.mutate });
  keys.current = { list, current, history, open: add.mutate, close: close.mutate };
  // Mod+1…9: the Nth tab, as a browser's.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const n = sessionJump(event);
      if (n === undefined) return;
      const { list, history } = keys.current;
      event.preventDefault();
      event.stopPropagation();
      const to = list[n - 1];
      if (!to) return;
      if (history) setHistory(false);
      showSession(project, to);
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [project]);
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const command = sessionShortcut(event);
      if (!command) return;
      const { list, current, history, open, close } = keys.current;
      event.preventDefault();
      event.stopPropagation();
      if (history && command !== 'new') return; // the history has no tabs to act on
      switch (command) {
        case 'new':
          open(current?.id);
          return;
        case 'close':
          if (current) close(current.id);
          return;
        case 'next':
        case 'previous': {
          const to = neighbour(list, current?.id, command === 'next' ? 1 : -1);
          if (to) showSession(project, to);
        }
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [project]);

  if (!sessions.data) {
    return box(<div className="flex h-full items-center justify-center text-sm text-subtle">{sessions.error ? t('chat.unavailable') : t('common.loading')}</div>);
  }

  if (history) {
    return box(
      <SessionHistory
        key={historyAt}
        project={project}
        reading={historyAt}
        onBack={() => {
          setHistory(false);
          setHistoryAt(undefined);
        }}
        onReopened={(s) => {
          setHistory(false);
          showSession(project, s);
        }}
        onContinued={opened}
      />,
    );
  }

  const closedCount = closed.data?.length ?? 0;
  const tabs = (
    <SessionTabs
      sessions={list}
      current={current?.id}
      adding={add.isPending}
      closedCount={closedCount}
      snoozed={sleeping}
      onWake={(id) => snooze.mutate({ id })}
      onChoose={(id) => {
        const s = list.find((x) => x.id === id);
        if (s) showSession(project, s);
      }}
      onAdd={() => add.mutate(current?.id)}
      onClose={(id) => close.mutate(id)}
      onHistory={() => openHistory()}
      onRename={(id, topic) => rename.mutate({ id, topic })}
      onPin={(id, pinned) => pin.mutate({ id, pinned })}
      onMove={(id, to) => reorder(moveTab(list, id, to))}
    />
  );

  if (!current) {
    return box(
      <div className="flex h-full min-h-0 flex-col">
        {/* Every open one snoozed: the row still lists them. */}
        {sleeping.length > 0 && tabs}
        <EmptyState
          icon={MessagesSquare}
          title={t('sessions.none')}
          action={
            <div className="flex flex-wrap justify-center gap-2">
              <Button variant="primary" onClick={() => add.mutate(undefined)} disabled={add.isPending} data-session-new title={keysHint(sessionKeys.new)}>
                <Plus className="size-4" />
                {t('sessions.new')}
              </Button>
              {closedCount > 0 && (
                <Button variant="secondary" onClick={() => openHistory()} data-session-history-open>
                  <History className="size-4" />
                  {t('sessions.historyCount', { count: closedCount })}
                </Button>
              )}
            </div>
          }
        >
          {t('sessions.noneHint')}
        </EmptyState>
      </div>,
    );
  }

  // A ref the chat answered with that isn't this session's is the one shown a
  // moment ago: wait for this one's.
  const ready = info && sessionOfRef(info.ref) === current.id;
  return (
    <div className="flex h-full min-h-0 flex-col" data-session-color={color}>
      {tabs}
      {current.parent && (
        <SessionParent
          parent={current.parent}
          onOpen={(parent) => {
            const open = list.find((x) => x.id === parent.session);
            if (open) {
              showSession(project, open);
              if (parent.item) requestReveal({ chat: { project, item: parent.item } });
              return;
            }
            setHistoryAt(parent.session);
            setHistory(true);
          }}
        />
      )}
      <div className="-mt-px flex min-h-0 flex-1 flex-col">
        {box(
          ready ? (
            <>
              <SessionHeader project={project} session={current} onSnooze={(until) => snooze.mutate({ id: current.id, until })} />
              {children(info, current)}
            </>
          ) : (
            <div className="flex h-full items-center justify-center text-sm text-subtle">{chat.error ? loadErrorMessage(chat.error, t('chat.unavailable')) : t('common.loading')}</div>
          ),
        )}
      </div>
    </div>
  );
}

// SessionParent is the line under the tabs of a session forked or continued
// from another: a link to that one, at the message it was forked at, or in
// the history when it is closed; plain words once it has been deleted.
function SessionParent({ parent, onOpen }: { parent: T.LeadSessionParent; onOpen: (parent: T.LeadSessionParent) => void }) {
  const t = useT();
  const forked = !!parent.item;
  return (
    <div className="flex min-w-0 items-center gap-1.5 px-3 pb-1.5 text-caption text-subtle" data-session-parent={parent.session}>
      <GitFork className="size-3.5 shrink-0" />
      {parent.topic ? (
        <button className="min-w-0 truncate rounded-sm underline-offset-2 transition-colors hover:text-tertiary hover:underline" title={t('sessions.parentHint')} onClick={() => onOpen(parent)}>
          {t(forked ? 'sessions.forkedFrom' : 'sessions.continuedFrom', { topic: parent.topic })}
        </button>
      ) : (
        <span className="min-w-0 truncate">{t(forked ? 'sessions.forkedFromGone' : 'sessions.continuedFromGone')}</span>
      )}
    </div>
  );
}
