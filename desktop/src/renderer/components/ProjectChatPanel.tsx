import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { History, MessagesSquare, Plus } from 'lucide-react';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import * as T from '../../shared/api';
import { api, sessionOfRef } from '../lib/api';
import { forgetThread } from '../lib/chat';
import { t, useT } from '../lib/i18n';
import { useChatOpenAt } from '../lib/reveal';
import { afterClose, chooseSession, markSessionSeen, neighbour, pickSession, useChosenSession, withSession } from '../lib/sessions';
import { requestSessionFocus } from '../lib/newTask';
import { keysHint, sessionKeys, sessionShortcut } from '../lib/shortcuts';
import { errorMessage } from '../lib/utils';
import { ChatTab } from './chat/ChatTab';
import { SessionHistory, useClosedSessions } from './chat/SessionHistory';
import { SessionTabs } from './chat/SessionTabs';
import { Button } from './ui/button';
import { EmptyState } from './ui/card';
import { useUiV2 } from '../lib/uiV2';
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
  return project ? pickSession(sessions.data ?? [], chosen) : undefined;
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
// What the project's agents reported is a thread per agent in the rail beside
// the whole view (AgentRail), rather than notices in the middle of it.
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
  const v2 = useUiV2();
  return (
    <SessionChatPanel project={project.name}>
      {(info, current) => (
        // Above the composer: in v2, how many of its agents wait for review.
        <ChatTab
          key={info.ref}
          agent={leadAgentFrom(project, info, current.topic)}
          starting={false}
          onStart={() => {}}
          openAt={openAt}
          aboveComposer={v2 && <ReviewCount project={project.name} variant="banner" />}
        />
      )}
    </SessionChatPanel>
  );
}

// SessionChatPanel is a chat in sessions, a project's or the Home chat's
// (project T.HomeProject): the tabs above it, their keys, the empty state of a
// chat with none open, and its history of closed sessions (SessionHistory),
// in the chat's place while it is open. children draws the session shown,
// once its chat has answered.
export function SessionChatPanel({ project, children }: { project: string; children: (info: T.ProjectChat, current: T.LeadSession) => ReactNode }) {
  const t = useT();
  const queryClient = useQueryClient();
  const sessions = useLeadSessions(project);
  const chosen = useChosenSession(project);
  const list = sessions.data ?? [];
  const current = pickSession(list, chosen);
  const closed = useClosedSessions(project);
  const [history, setHistory] = useState(false);
  const chat = useQuery({
    queryKey: ['projectChat', project, current?.id],
    queryFn: () => api.projectChat(project, current!.id),
    enabled: !!current,
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
  const rename = useMutation({
    mutationFn: ({ id, topic }: { id: string; topic: string }) => api.renameLeadSession(project, id, topic),
    onSuccess: refresh,
    onError: (err) => toast.error(errorMessage(err)),
  });

  // What is on screen is seen: now, and whenever it says more.
  const shownId = current?.id;
  const shownLast = current?.last?.at;
  useEffect(() => {
    if (shownId) markSessionSeen(project, shownId);
  }, [project, shownId, shownLast]);

  // The sessions' keys (lib/shortcuts.ts), while this chat is on screen.
  const keys = useRef({ list, current, history, open: add.mutate, close: close.mutate });
  keys.current = { list, current, history, open: add.mutate, close: close.mutate };
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
    return <div className="flex h-full items-center justify-center text-sm text-subtle">{sessions.error ? t('chat.unavailable') : t('common.loading')}</div>;
  }

  if (history) {
    return (
      <SessionHistory
        project={project}
        onBack={() => setHistory(false)}
        onReopened={(s) => {
          setHistory(false);
          showSession(project, s);
        }}
        onContinued={opened}
      />
    );
  }

  const closedCount = closed.data?.length ?? 0;
  const tabs = (
    <SessionTabs
      sessions={list}
      current={current?.id}
      adding={add.isPending}
      closedCount={closedCount}
      onChoose={(id) => {
        const s = list.find((x) => x.id === id);
        if (s) showSession(project, s);
      }}
      onAdd={() => add.mutate(current?.id)}
      onClose={(id) => close.mutate(id)}
      onHistory={() => setHistory(true)}
      onRename={(id, topic) => rename.mutate({ id, topic })}
    />
  );

  if (!current) {
    return (
      <div className="flex h-full min-h-0 flex-col">
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
                <Button variant="secondary" onClick={() => setHistory(true)} data-session-history-open>
                  <History className="size-4" />
                  {t('sessions.historyCount', { count: closedCount })}
                </Button>
              )}
            </div>
          }
        >
          {t('sessions.noneHint')}
        </EmptyState>
      </div>
    );
  }

  // A ref the chat answered with that isn't this session's is the one shown a
  // moment ago: wait for this one's.
  const ready = info && sessionOfRef(info.ref) === current.id;
  return (
    <div className="flex h-full min-h-0 flex-col">
      {tabs}
      <div className="relative flex min-h-0 flex-1 flex-col">
        {ready ? (
          children(info, current)
        ) : (
          <div className="flex h-full items-center justify-center text-sm text-subtle">{chat.error ? t('chat.unavailable') : t('common.loading')}</div>
        )}
      </div>
    </div>
  );
}
