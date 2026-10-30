import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { MessagesSquare, Plus } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import * as T from '../../shared/api';
import { api, sessionOfRef } from '../lib/api';
import { t, useT } from '../lib/i18n';
import { useChatOpenAt } from '../lib/reveal';
import { afterDelete, chooseSession, markSessionSeen, neighbour, pickSession, useChosenSession } from '../lib/sessions';
import { keysHint, sessionKeys, sessionShortcut } from '../lib/shortcuts';
import { errorMessage } from '../lib/utils';
import { ChatTab } from './chat/ChatTab';
import { SessionTabs, TopicInput } from './chat/SessionTabs';
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

// useLeadSessions is a project's chat sessions, kept current by its events
// (lib/events.ts).
export function useLeadSessions(project: string) {
  return useQuery({ queryKey: ['leadSessions', project], queryFn: () => api.leadSessions(project) });
}

// useCurrentSession is the session a project's chat shows, the one its tabs
// have chosen; undefined while they load, or with none.
export function useCurrentSession(project: string | undefined): T.LeadSession | undefined {
  const sessions = useQuery({ queryKey: ['leadSessions', project], queryFn: () => api.leadSessions(project!), enabled: !!project });
  const chosen = useChosenSession(project ?? '');
  return project ? pickSession(sessions.data ?? [], chosen) : undefined;
}

// The project's chat, driven by its leads: one per session, the one shown
// picked in the tabs above it. ChatTab is shared with the agents' chats, so
// the lead is handed to it as an agent.
//
// The conversation is what you and the lead said to each other, and only that.
// What the project's agents reported is a thread per agent in the rail beside
// the whole view (AgentRail), rather than notices in the middle of it.
//
// Opening it starts the lead's AI tool, with no turn, so its settings can be
// chosen before the first message. A session whose chat is never opened costs
// nothing; a project with no session shows an empty state that opens one.
export function ProjectChatPanel({ project }: { project: T.Project }) {
  const t = useT();
  const queryClient = useQueryClient();
  const sessions = useLeadSessions(project.name);
  const chosen = useChosenSession(project.name);
  const list = sessions.data ?? [];
  const current = pickSession(list, chosen);
  const [adding, setAdding] = useState(false);
  const [confirming, setConfirming] = useState<string>();
  const chat = useQuery({
    queryKey: ['projectChat', project.name, current?.id],
    queryFn: () => api.projectChat(project.name, current!.id),
    enabled: !!current,
  });
  const info = chat.data;
  const openAt = useChatOpenAt(project.name);
  const v2 = useUiV2();

  const refresh = () => void queryClient.invalidateQueries({ queryKey: ['leadSessions', project.name] });
  const add = useMutation({
    mutationFn: (topic: string) => api.newLeadSession(project.name, topic),
    onSuccess: (s) => {
      setAdding(false);
      chooseSession(project.name, s.id);
      refresh();
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.deleteLeadSession(project.name, id),
    onMutate: (id) => {
      setConfirming(undefined);
      const next = afterDelete(list, id);
      if (next && id === current?.id) chooseSession(project.name, next.id);
    },
    onSuccess: () => {
      refresh();
      void queryClient.invalidateQueries({ queryKey: ['agents'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const rename = useMutation({
    mutationFn: ({ id, topic }: { id: string; topic: string }) => api.renameLeadSession(project.name, id, topic),
    onSuccess: refresh,
    onError: (err) => toast.error(errorMessage(err)),
  });

  // What is on screen is seen: now, and whenever it says more.
  const shownId = current?.id;
  const shownLast = current?.last?.at;
  useEffect(() => {
    if (shownId) markSessionSeen(project.name, shownId);
  }, [project.name, shownId, shownLast]);

  // The sessions' keys (lib/shortcuts.ts), while this chat is on screen.
  const keys = useRef({ list, current, adding });
  keys.current = { list, current, adding };
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const command = sessionShortcut(event);
      if (!command) return;
      const { list, current } = keys.current;
      event.preventDefault();
      event.stopPropagation();
      switch (command) {
        case 'new':
          setConfirming(undefined);
          setAdding(true);
          return;
        case 'delete':
          if (current) setConfirming(current.id);
          return;
        case 'next':
        case 'previous': {
          const to = neighbour(list, current?.id, command === 'next' ? 1 : -1);
          if (to) chooseSession(project.name, to.id);
        }
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [project.name]);

  if (!sessions.data) {
    return <div className="flex h-full items-center justify-center text-sm text-subtle">{sessions.error ? t('chat.unavailable') : t('common.loading')}</div>;
  }

  const tabs = (
    <SessionTabs
      sessions={list}
      current={current?.id}
      adding={adding}
      confirming={confirming}
      onChoose={(id) => {
        setConfirming(undefined);
        chooseSession(project.name, id);
      }}
      onAdd={(topic) => add.mutate(topic)}
      onAdding={setAdding}
      onConfirm={setConfirming}
      onDelete={(id) => remove.mutate(id)}
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
            adding ? (
              <TopicInput label={t('sessions.topic')} placeholder={t('sessions.topicPlaceholder')} onDone={(topic) => (topic ? add.mutate(topic) : setAdding(false))} />
            ) : (
              <Button variant="primary" onClick={() => setAdding(true)} data-session-new title={keysHint(sessionKeys.new)}>
                <Plus className="size-4" />
                {t('sessions.new')}
              </Button>
            )
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
          // Above the composer: in v2, how many of its agents wait for review.
          <ChatTab
            key={info.ref}
            agent={leadAgentFrom(project, info, current.topic)}
            starting={false}
            onStart={() => {}}
            openAt={openAt}
            aboveComposer={v2 && <ReviewCount project={project.name} variant="banner" />}
          />
        ) : (
          <div className="flex h-full items-center justify-center text-sm text-subtle">{chat.error ? t('chat.unavailable') : t('common.loading')}</div>
        )}
      </div>
    </div>
  );
}
