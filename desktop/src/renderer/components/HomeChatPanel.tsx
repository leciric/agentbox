import { Brain, MessagesSquare } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import * as T from '../../shared/api';
import { api } from '../lib/api';
import { t, useT } from '../lib/i18n';
import { useChatOpenAt } from '../lib/reveal';
import { ChatHeaderControls, ChatTab } from './chat/ChatTab';
import { GlobalMemoryPanel } from './GlobalMemoryPanel';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs';

// homeAgentFrom builds the Agent ChatTab expects out of the Home chat: a lead
// like a project's, kept under T.HomeProject, standing in ~/.agentbox rather
// than in a worktree.
export function homeAgentFrom(info: T.ProjectChat): T.Agent {
  return {
    ref: info.ref,
    project: T.HomeProject,
    name: T.LeadName,
    title: t('chat.home.agentTitle'),
    instance: '',
    ai: 'claude',
    autonomous: false,
    branch: '',
    baseRef: '',
    baseCommit: '',
    worktree: info.worktree || '',
    source: '',
    claudeAccount: '',
    githubAccount: '',
    interface: 'chat',
    chat: info.chat,
    state: 'running', // no machine: the chat can always run
    ip: '',
    createdAt: '',
  };
}

// The Home chat: the user's main chat, across every project and tied to none.
// Its tools list the projects and their agents, create agents, tell a
// project's own chat something, search any project's memory and add projects,
// so it's there even before the first project is.
export function HomeChatPanel() {
  const t = useT();
  const chat = useQuery({ queryKey: ['projectChat', T.HomeProject], queryFn: () => api.projectChat(T.HomeProject) });
  const info = chat.data;
  const openAt = useChatOpenAt(T.HomeProject);
  return (
    <Tabs defaultValue="chat" className="flex h-full min-h-0 flex-col">
      <div className="shrink-0 px-4 pb-3 pt-5 md:px-8">
        <div className="flex flex-wrap items-center gap-3">
          <div className="min-w-0">
            <h1 className="text-xl font-semibold tracking-tight text-title">{t('chat.home.title')}</h1>
            <p className="mt-0.5 text-[13px] text-muted">{t('chat.home.subtitle')}</p>
          </div>
        </div>
        <div className="mt-3 flex items-center gap-3">
          <TabsList className="min-w-0 flex-1 overflow-x-auto [scrollbar-width:none]">
            <TabsTrigger value="chat">
              <MessagesSquare />
              {t('project.view.tab.chat')}
            </TabsTrigger>
            <TabsTrigger value="memory">
              <Brain />
              {t('chat.home.tab.memory')}
            </TabsTrigger>
          </TabsList>
          {info && (
            <div className="flex shrink-0 items-center gap-2">
              <ChatHeaderControls agent={homeAgentFrom(info)} />
            </div>
          )}
        </div>
      </div>
      <TabsContent value="chat" className="flex flex-col">
        <div className="mx-2 mb-2 flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-line md:mx-6 md:mb-6">
          {info ? (
            <ChatTab agent={homeAgentFrom(info)} starting={false} onStart={() => {}} openAt={openAt} />
          ) : (
            <div className="flex h-full items-center justify-center text-sm text-subtle">{chat.error ? t('chat.unavailable') : t('common.loading')}</div>
          )}
        </div>
      </TabsContent>
      <TabsContent value="memory" className="overflow-y-auto">
        <div className="mx-auto max-w-4xl px-4 py-2 md:px-8 md:pb-7">
          <p className="mb-3 text-[13px] text-muted">{t('chat.home.memoryIntro')}</p>
          <GlobalMemoryPanel />
        </div>
      </TabsContent>
    </Tabs>
  );
}
